// Package mdns is a minimal multicast DNS participant: enough to announce one
// service and find others, with the socket setup both target platforms need.
//
// It exists because the libraries we tried give no control over the *sending*
// socket, and on macOS an unscoped socket cannot send to 224.0.0.0/4 at all.
// Since answering a query is itself a send, that made our responder invisible.
// See tech-specs/decisions.md, "Discovery and names".
package mdns

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
	"golang.org/x/net/ipv4"
)

const (
	// Port is the mDNS port, shared with the platform responder.
	Port = 5353

	// defaultTTL is what RFC 6762 recommends for shared records.
	defaultTTL = 120

	readBuffer = 9000
)

var groupV4 = net.IPv4(224, 0, 0, 251)

// Entry is a discovered service instance.
type Entry struct {
	Instance string
	Host     string
	Port     int
	Text     []string
	Addrs    []netip.Addr
}

// Service is what this machine advertises.
type Service struct {
	Instance string // e.g. "desktop"
	Type     string // e.g. "_jaggery._tcp"
	Host     string // e.g. "desktop.local."
	Port     int
	Text     []string
	Addrs    []netip.Addr
}

func (s Service) instanceFQDN() string {
	return fmt.Sprintf("%s.%s.local.", s.Instance, s.Type)
}

func (s Service) typeFQDN() string {
	return s.Type + ".local."
}

type handler func(*dns.Msg, netip.Addr)

// Conn is a bound mDNS socket. One per agent; queries and responses share it
// because only one socket can usefully hold the port.
type Conn struct {
	pc     *ipv4.PacketConn
	ifaces []net.Interface

	mu       sync.Mutex
	handlers map[int]handler
	nextID   int
	closed   bool
}

// Open binds port 5353 and joins the mDNS group on every supplied interface.
func Open(ifaces []net.Interface) (*Conn, error) {
	if len(ifaces) == 0 {
		return nil, errors.New("mdns: no interfaces to listen on")
	}

	lc := net.ListenConfig{Control: control}
	packetConn, err := lc.ListenPacket(context.Background(), "udp4", fmt.Sprintf("0.0.0.0:%d", Port))
	if err != nil {
		return nil, fmt.Errorf("mdns: binding port %d: %w", Port, err)
	}

	pc := ipv4.NewPacketConn(packetConn)
	joined := make([]net.Interface, 0, len(ifaces))
	var joinErrs []error
	for _, iface := range ifaces {
		if err := pc.JoinGroup(&iface, &net.UDPAddr{IP: groupV4}); err != nil {
			joinErrs = append(joinErrs, fmt.Errorf("%s: %w", iface.Name, err))
			continue
		}
		joined = append(joined, iface)
	}
	if len(joined) == 0 {
		pc.Close()
		return nil, fmt.Errorf("mdns: could not join the group on any interface: %w", errors.Join(joinErrs...))
	}

	_ = pc.SetMulticastTTL(255)
	_ = pc.SetMulticastLoopback(true)

	return &Conn{pc: pc, ifaces: joined, handlers: make(map[int]handler)}, nil
}

// Interfaces reports the interfaces the group was actually joined on.
func (c *Conn) Interfaces() []net.Interface {
	return c.ifaces
}

func (c *Conn) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return c.pc.Close()
}

func (c *Conn) addHandler(h handler) (remove func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.nextID
	c.nextID++
	c.handlers[id] = h
	return func() {
		c.mu.Lock()
		delete(c.handlers, id)
		c.mu.Unlock()
	}
}

// Run reads packets until the context is cancelled or the conn is closed.
func (c *Conn) Run(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		c.pc.SetReadDeadline(time.Now())
	}()

	buf := make([]byte, readBuffer)
	for {
		n, _, src, err := c.pc.ReadFrom(buf)
		if err != nil {
			c.mu.Lock()
			closed := c.closed
			c.mu.Unlock()
			if ctx.Err() != nil || closed {
				return nil
			}
			var nerr net.Error
			if errors.As(err, &nerr) && nerr.Timeout() {
				continue
			}
			return fmt.Errorf("mdns: read: %w", err)
		}

		msg := new(dns.Msg)
		if msg.Unpack(buf[:n]) != nil {
			continue
		}

		from, _ := netip.AddrFromSlice(net.IP(nil))
		if udp, ok := src.(*net.UDPAddr); ok {
			if a, ok := netip.AddrFromSlice(udp.IP); ok {
				from = a.Unmap()
			}
		}

		c.mu.Lock()
		snapshot := make([]handler, 0, len(c.handlers))
		for _, h := range c.handlers {
			snapshot = append(snapshot, h)
		}
		c.mu.Unlock()

		for _, h := range snapshot {
			h(msg, from)
		}
	}
}

// send writes a message to the group on every joined interface, setting the
// outbound interface each time. Without that, macOS has no route and fails.
func (c *Conn) send(msg *dns.Msg) error {
	raw, err := msg.Pack()
	if err != nil {
		return fmt.Errorf("mdns: packing message: %w", err)
	}

	dst := &net.UDPAddr{IP: groupV4, Port: Port}
	var errs []error
	for _, iface := range c.ifaces {
		if err := c.pc.SetMulticastInterface(&iface); err != nil {
			errs = append(errs, fmt.Errorf("%s: scoping: %w", iface.Name, err))
			continue
		}
		if _, err := c.pc.WriteTo(raw, nil, dst); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", iface.Name, err))
		}
	}
	if len(errs) == len(c.ifaces) {
		return fmt.Errorf("mdns: send failed on every interface: %w", errors.Join(errs...))
	}
	return nil
}

// Query asks for a service type and collects answers until the timeout.
func (c *Conn) Query(ctx context.Context, serviceType string, timeout time.Duration) ([]Entry, error) {
	var (
		mu       sync.Mutex
		gathered = make(map[string]*Entry)
	)

	remove := c.addHandler(func(msg *dns.Msg, _ netip.Addr) {
		if len(msg.Answer) == 0 && len(msg.Extra) == 0 {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		collect(gathered, msg, serviceType+".local.")
	})
	defer remove()

	question := new(dns.Msg)
	question.SetQuestion(serviceType+".local.", dns.TypePTR)
	// RFC 6762: multicast queries carry a zero ID and no recursion flag.
	question.Id = 0
	question.RecursionDesired = false

	if err := c.send(question); err != nil {
		return nil, err
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}

	mu.Lock()
	defer mu.Unlock()
	entries := make([]Entry, 0, len(gathered))
	for _, e := range gathered {
		if e.Host != "" || len(e.Addrs) > 0 {
			entries = append(entries, *e)
		}
	}
	return entries, nil
}

// collect folds the records of one message into entries keyed by instance.
func collect(into map[string]*Entry, msg *dns.Msg, typeFQDN string) {
	entryFor := func(instance string) *Entry {
		if e, ok := into[instance]; ok {
			return e
		}
		e := &Entry{Instance: instance}
		into[instance] = e
		return e
	}

	hostToInstance := make(map[string]string)
	records := append(append([]dns.RR{}, msg.Answer...), msg.Extra...)

	for _, rr := range records {
		if ptr, ok := rr.(*dns.PTR); ok && equalName(ptr.Hdr.Name, typeFQDN) {
			entryFor(trimInstance(ptr.Ptr, typeFQDN))
		}
	}

	for _, rr := range records {
		switch v := rr.(type) {
		case *dns.SRV:
			instance := trimInstance(v.Hdr.Name, typeFQDN)
			if _, known := into[instance]; !known {
				continue
			}
			e := entryFor(instance)
			e.Host, e.Port = v.Target, int(v.Port)
			hostToInstance[dns.CanonicalName(v.Target)] = instance
		case *dns.TXT:
			instance := trimInstance(v.Hdr.Name, typeFQDN)
			if _, known := into[instance]; known {
				entryFor(instance).Text = append(entryFor(instance).Text, v.Txt...)
			}
		}
	}

	for _, rr := range records {
		a, ok := rr.(*dns.A)
		if !ok {
			continue
		}
		instance, ok := hostToInstance[dns.CanonicalName(a.Hdr.Name)]
		if !ok {
			continue
		}
		if addr, ok := netip.AddrFromSlice(a.A); ok {
			e := entryFor(instance)
			addr = addr.Unmap()
			if !containsAddr(e.Addrs, addr) {
				e.Addrs = append(e.Addrs, addr)
			}
		}
	}
}

// Serve announces a service and answers queries for it until ctx is done.
func (c *Conn) Serve(ctx context.Context, svc Service) error {
	remove := c.addHandler(func(msg *dns.Msg, _ netip.Addr) {
		if msg.Response || len(msg.Question) == 0 {
			return
		}
		for _, q := range msg.Question {
			if !c.shouldAnswer(q, svc) {
				continue
			}
			if err := c.send(response(svc)); err != nil {
				return
			}
			return
		}
	})
	defer remove()

	// Announce unsolicited so peers learn about us without having to ask.
	if err := c.send(response(svc)); err != nil {
		return err
	}

	ticker := time.NewTicker(time.Duration(defaultTTL/2) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := c.send(response(svc)); err != nil {
				return err
			}
		}
	}
}

func (c *Conn) shouldAnswer(q dns.Question, svc Service) bool {
	switch q.Qtype {
	case dns.TypePTR:
		return equalName(q.Name, svc.typeFQDN())
	case dns.TypeSRV, dns.TypeTXT, dns.TypeANY:
		return equalName(q.Name, svc.instanceFQDN())
	case dns.TypeA:
		return equalName(q.Name, svc.Host)
	default:
		return false
	}
}

func response(svc Service) *dns.Msg {
	msg := new(dns.Msg)
	msg.Response = true
	msg.Authoritative = true

	instance := svc.instanceFQDN()
	msg.Answer = []dns.RR{
		&dns.PTR{
			Hdr: dns.RR_Header{Name: svc.typeFQDN(), Rrtype: dns.TypePTR, Class: dns.ClassINET, Ttl: defaultTTL},
			Ptr: instance,
		},
		&dns.SRV{
			Hdr:    dns.RR_Header{Name: instance, Rrtype: dns.TypeSRV, Class: dns.ClassINET, Ttl: defaultTTL},
			Target: svc.Host,
			Port:   uint16(svc.Port),
		},
		&dns.TXT{
			Hdr: dns.RR_Header{Name: instance, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: defaultTTL},
			Txt: svc.Text,
		},
	}

	for _, addr := range svc.Addrs {
		if !addr.Is4() {
			continue
		}
		msg.Extra = append(msg.Extra, &dns.A{
			Hdr: dns.RR_Header{Name: svc.Host, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: defaultTTL},
			A:   net.IP(addr.AsSlice()),
		})
	}
	return msg
}

func equalName(a, b string) bool {
	return dns.CanonicalName(a) == dns.CanonicalName(b)
}

func containsAddr(addrs []netip.Addr, want netip.Addr) bool {
	for _, a := range addrs {
		if a == want {
			return true
		}
	}
	return false
}

// trimInstance turns "desktop._jaggery._tcp.local." into "desktop".
func trimInstance(name, typeFQDN string) string {
	canonical := dns.CanonicalName(name)
	suffix := "." + dns.CanonicalName(typeFQDN)
	if len(canonical) > len(suffix) && canonical[len(canonical)-len(suffix):] == suffix {
		return canonical[:len(canonical)-len(suffix)]
	}
	return canonical
}

// LookupAddr asks the network which .local name holds addr (a reverse PTR
// query), the way phones and laptops name themselves. Empty when nothing
// answers before the timeout.
func (c *Conn) LookupAddr(ctx context.Context, addr netip.Addr, timeout time.Duration) (string, error) {
	reverse, err := dns.ReverseAddr(addr.String())
	if err != nil {
		return "", err
	}
	found := make(chan string, 1)
	remove := c.addHandler(func(msg *dns.Msg, _ netip.Addr) {
		for _, rr := range append(msg.Answer, msg.Extra...) {
			if ptr, ok := rr.(*dns.PTR); ok && equalName(ptr.Hdr.Name, reverse) {
				select {
				case found <- strings.TrimSuffix(ptr.Ptr, "."):
				default:
				}
				return
			}
		}
	})
	defer remove()

	question := new(dns.Msg)
	question.SetQuestion(reverse, dns.TypePTR)
	question.Id = 0
	question.RecursionDesired = false
	if err := c.send(question); err != nil {
		return "", err
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case name := <-found:
		return name, nil
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timer.C:
		return "", nil
	}
}
