package mdns

import (
	"net/netip"
	"testing"

	"github.com/miekg/dns"
)

func testService() Service {
	return Service{
		Instance: "desktop",
		Type:     "_jaggery._tcp",
		Host:     "desktop.local.",
		Port:     7373,
		Text:     []string{"jag=1", "id=a240859d960f", "name=desktop"},
		Addrs:    []netip.Addr{netip.MustParseAddr("192.0.2.65")},
	}
}

// What we advertise must be exactly what a peer reads back.
func TestAnnouncementRoundTrip(t *testing.T) {
	svc := testService()

	gathered := map[string]*Entry{}
	collect(gathered, response(svc), svc.typeFQDN())

	entry, ok := gathered["desktop"]
	if !ok {
		t.Fatalf("instance not found; got %v", gathered)
	}
	if entry.Port != svc.Port {
		t.Errorf("port = %d, want %d", entry.Port, svc.Port)
	}
	if !equalName(entry.Host, svc.Host) {
		t.Errorf("host = %q, want %q", entry.Host, svc.Host)
	}
	if len(entry.Addrs) != 1 || entry.Addrs[0].String() != "192.0.2.65" {
		t.Errorf("addrs = %v, want the advertised v4 address", entry.Addrs)
	}
	if len(entry.Text) != len(svc.Text) {
		t.Errorf("text = %v, want %v", entry.Text, svc.Text)
	}
}

func TestResponseCarriesOnlyIPv4Addresses(t *testing.T) {
	svc := testService()
	svc.Addrs = append(svc.Addrs, netip.MustParseAddr("fd00::1"))

	var aRecords int
	for _, rr := range response(svc).Extra {
		if _, ok := rr.(*dns.A); ok {
			aRecords++
		}
	}
	if aRecords != 1 {
		t.Errorf("got %d A records, want 1 (the v6 address has no A record)", aRecords)
	}
}

func TestCollectIgnoresOtherServiceTypes(t *testing.T) {
	msg := new(dns.Msg)
	msg.Response = true
	msg.Answer = []dns.RR{
		&dns.PTR{
			Hdr: dns.RR_Header{Name: "_raop._tcp.local.", Rrtype: dns.TypePTR, Class: dns.ClassINET, Ttl: 120},
			Ptr: "Kitchen._raop._tcp.local.",
		},
	}

	gathered := map[string]*Entry{}
	collect(gathered, msg, "_jaggery._tcp.local.")

	if len(gathered) != 0 {
		t.Errorf("collected %v from an unrelated service type", gathered)
	}
}

// A stray SRV without a matching PTR must not invent an instance.
func TestCollectRequiresAPointerRecord(t *testing.T) {
	msg := new(dns.Msg)
	msg.Response = true
	msg.Answer = []dns.RR{
		&dns.SRV{
			Hdr:    dns.RR_Header{Name: "ghost._jaggery._tcp.local.", Rrtype: dns.TypeSRV, Class: dns.ClassINET, Ttl: 120},
			Target: "ghost.local.",
			Port:   7373,
		},
	}

	gathered := map[string]*Entry{}
	collect(gathered, msg, "_jaggery._tcp.local.")

	if len(gathered) != 0 {
		t.Errorf("collected %v from an SRV with no PTR", gathered)
	}
}

// A responder re-sends the same announcement periodically; merging two of them
// must not accumulate duplicate addresses. Regression for the doubled address
// seen in the first two-machine run.
func TestCollectDedupesAddressesAcrossMessages(t *testing.T) {
	svc := testService()
	gathered := map[string]*Entry{}
	collect(gathered, response(svc), svc.typeFQDN())
	collect(gathered, response(svc), svc.typeFQDN())

	entry := gathered["desktop"]
	if entry == nil {
		t.Fatal("instance not found")
	}
	if len(entry.Addrs) != 1 {
		t.Errorf("addrs = %v, want a single deduped address", entry.Addrs)
	}
}

func TestShouldAnswer(t *testing.T) {
	svc := testService()
	conn := &Conn{}

	tests := []struct {
		name string
		q    dns.Question
		want bool
	}{
		{"PTR for our service type", dns.Question{Name: "_jaggery._tcp.local.", Qtype: dns.TypePTR}, true},
		{"PTR for another type", dns.Question{Name: "_raop._tcp.local.", Qtype: dns.TypePTR}, false},
		{"SRV for our instance", dns.Question{Name: "desktop._jaggery._tcp.local.", Qtype: dns.TypeSRV}, true},
		{"TXT for our instance", dns.Question{Name: "desktop._jaggery._tcp.local.", Qtype: dns.TypeTXT}, true},
		{"SRV for another instance", dns.Question{Name: "nas._jaggery._tcp.local.", Qtype: dns.TypeSRV}, false},
		{"A for our host", dns.Question{Name: "desktop.local.", Qtype: dns.TypeA}, true},
		{"A for another host", dns.Question{Name: "nas.local.", Qtype: dns.TypeA}, false},
		{"name matching is case-insensitive", dns.Question{Name: "_JAGGERY._TCP.local.", Qtype: dns.TypePTR}, true},
		{"unsupported type", dns.Question{Name: "_jaggery._tcp.local.", Qtype: dns.TypeMX}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := conn.shouldAnswer(tt.q, svc); got != tt.want {
				t.Errorf("shouldAnswer(%q, %d) = %v, want %v", tt.q.Name, tt.q.Qtype, got, tt.want)
			}
		})
	}
}

func TestTrimInstance(t *testing.T) {
	tests := []struct{ in, want string }{
		{"desktop._jaggery._tcp.local.", "desktop"},
		{"Alexs-MacBook-laptop._jaggery._tcp.local.", "alexs-macbook-laptop"},
		{"_jaggery._tcp.local.", "_jaggery._tcp.local."},
	}
	for _, tt := range tests {
		if got := trimInstance(tt.in, "_jaggery._tcp.local."); got != tt.want {
			t.Errorf("trimInstance(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestOpenRefusesWithoutInterfaces(t *testing.T) {
	if _, err := Open(nil); err == nil {
		t.Error("Open(nil) should fail rather than bind nothing silently")
	}
}
