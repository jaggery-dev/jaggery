// Package reach answers "can the rest of the house reach me?" — the check the
// MVP spike found missing (tech-specs/decisions.md, "Firewalls and ports"). A host firewall that
// drops the agent's ports leaves names resolving and pages not loading, and
// the person blames Jaggery. Only a dial from another machine tells the truth,
// so an agent asks an approved peer to dial it back, and reports blocked ports
// with the command that opens them.
package reach

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/jaggery-dev/jaggery/internal/airuntime"
)

// Path is the secured endpoint a peer calls to be dialled back.
const Path = "/v1/reach"

// Request names the ports to dial back, and the AI runtimes to look for.
type Request struct {
	Ports []int `json:"ports"`
	// Runtimes asks the peer to try each runtime's default port over
	// HTTP. Empty from agents older than the check.
	Runtimes []airuntime.Kind `json:"runtimes,omitempty"`
	// IPv6 are the caller's link-local IPv6 addresses (no zone): the peer
	// dials Ports there too, through its own LAN interfaces.
	IPv6 []string `json:"ipv6,omitempty"`
}

// RuntimeCheck says whether an AI runtime on the checked machine answers the
// whole network. One that does bypasses Jaggery: anyone on the LAN can use
// its management API to pull, delete or replace models.
type RuntimeCheck struct {
	Kind    airuntime.Kind `json:"kind"`
	Port    int            `json:"port"`
	Exposed bool           `json:"exposed"`
	// Fix is how to bind it to this machine only, filled in by the checked
	// machine, which knows its OS.
	Fix string `json:"fix,omitempty"`
}

type Check struct {
	Port  int    `json:"port"`
	Open  bool   `json:"open"`
	Error string `json:"error,omitempty"`
	// Fix is the command that opens a blocked port, filled in by the machine
	// that was checked: only it knows its firewall and subnet.
	Fix string `json:"fix,omitempty"`
}

// Report is the result of one dial-back, as seen by the peer that dialled.
type Report struct {
	CheckedBy string    `json:"checked_by"`
	CheckedAt time.Time `json:"checked_at"`
	Checks    []Check   `json:"checks"`
	// Runtimes is nil when the peer did not check them (an older agent).
	Runtimes []RuntimeCheck `json:"runtimes,omitempty"`
	// Checks6 are the same ports over IPv6 link-local; nil when not checked.
	Checks6 []Check `json:"checks6,omitempty"`
}

// IPv6Open reports a check over IPv6 that reached port.
func (r *Report) IPv6Open(port int) bool {
	if r == nil {
		return false
	}
	for _, c := range r.Checks6 {
		if c.Port == port && c.Open {
			return true
		}
	}
	return false
}

// DialIPv6 tries ports on a caller's link-local addresses. A link-local
// address only means something on one link, so it is dialled through each
// of zones (this machine's LAN interfaces) until one connects.
func DialIPv6(ctx context.Context, addrs []string, zones []string, ports []int, timeout time.Duration) []Check {
	var checks []Check
	for _, p := range ports {
		c := Check{Port: p}
		d := net.Dialer{Timeout: timeout}
	tries:
		for _, a := range addrs {
			ip, err := netip.ParseAddr(a)
			if err != nil || !ip.Is6() || !ip.IsLinkLocalUnicast() {
				continue // only link-local: the endpoint can't be aimed elsewhere
			}
			for _, z := range zones {
				conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip.WithZone(z).String(), strconv.Itoa(p)))
				if err == nil {
					conn.Close()
					c.Open, c.Error = true, ""
					break tries
				}
				c.Error = err.Error()
			}
		}
		checks = append(checks, c)
	}
	return checks
}

// Exposed lists the runtimes the whole network can reach.
func (r *Report) Exposed() []RuntimeCheck {
	if r == nil {
		return nil
	}
	var out []RuntimeCheck
	for _, c := range r.Runtimes {
		if c.Exposed {
			out = append(out, c)
		}
	}
	return out
}

// CheckRuntimes looks for each requested runtime on host's default port. A
// runtime counts as exposed only if it answers in that runtime's own shape,
// so an unrelated app on 8080 is not mistaken for llama.cpp. Unknown kinds
// are ignored, so a peer can't point this at arbitrary ports.
func CheckRuntimes(ctx context.Context, host netip.Addr, kinds []airuntime.Kind, ports map[airuntime.Kind]int) []RuntimeCheck {
	want := map[airuntime.Kind]int{}
	for _, k := range kinds {
		if port, ok := ports[k]; ok {
			want[k] = port
		}
	}
	found := map[airuntime.Kind]bool{}
	for _, rt := range airuntime.DetectAt(ctx, host.String(), want) {
		found[rt.Kind] = true
	}
	checks := make([]RuntimeCheck, 0, len(want))
	for _, k := range kinds {
		port, ok := want[k]
		if !ok {
			continue
		}
		checks = append(checks, RuntimeCheck{Kind: k, Port: port, Exposed: found[k]})
	}
	return checks
}

// RuntimeFix is how to bind a runtime to this machine only, for goos. Jaggery
// reaches runtimes on 127.0.0.1 and other machines reach them through Jaggery
// over mutual TLS, so nothing in the house loses access.
func RuntimeFix(kind airuntime.Kind, goos string) string {
	switch kind {
	case airuntime.Ollama:
		switch goos {
		case "darwin":
			return "launchctl setenv OLLAMA_HOST 127.0.0.1, then quit and reopen Ollama"
		case "windows":
			return "setx OLLAMA_HOST 127.0.0.1, then quit and reopen Ollama"
		default:
			return `started by hand: stop it and run plain "ollama serve" (no OLLAMA_HOST, or OLLAMA_HOST=127.0.0.1); as a service: sudo systemctl edit ollama, add [Service] Environment="OLLAMA_HOST=127.0.0.1", then sudo systemctl restart ollama; in Docker: publish -p 127.0.0.1:11434:11434`
		}
	case airuntime.LMStudio:
		return `in LM Studio's Developer tab, turn off "Serve on Local Network"`
	case airuntime.LlamaCpp:
		return "restart llama-server without --host 0.0.0.0 (it listens on 127.0.0.1 by default)"
	case airuntime.VLLM:
		return "restart vllm serve with --host 127.0.0.1; in Docker: publish -p 127.0.0.1:8000:8000"
	}
	return fmt.Sprintf("bind %s to 127.0.0.1", kind)
}

// Blocked lists the ports the dial-back could not reach.
func (r *Report) Blocked() []int {
	if r == nil {
		return nil
	}
	var out []int
	for _, c := range r.Checks {
		if !c.Open {
			out = append(out, c.Port)
		}
	}
	return out
}

// Allowed restricts what a peer may ask us to dial to the agent's own ports,
// so the endpoint can't be used to probe arbitrary services on the caller.
func Allowed(ports []int, permitted ...int) []int {
	var out []int
	for _, p := range ports {
		for _, ok := range permitted {
			if p == ok {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// Dial tries each port on host and reports which accepted a connection.
func Dial(ctx context.Context, host netip.Addr, ports []int, timeout time.Duration) []Check {
	checks := make([]Check, 0, len(ports))
	d := net.Dialer{Timeout: timeout}
	for _, p := range ports {
		conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host.String(), strconv.Itoa(p)))
		c := Check{Port: p, Open: err == nil}
		if err != nil {
			c.Error = err.Error()
		} else {
			conn.Close()
		}
		checks = append(checks, c)
	}
	return checks
}

// Firewall is the host firewall a fix should be written for.
type Firewall string

const (
	UFW       Firewall = "ufw"
	Firewalld Firewall = "firewalld"
	Unknown   Firewall = ""
)

// DetectFirewall guesses by which tool is installed: reading the active rules
// needs root, and the hint only needs to be in the right dialect.
func DetectFirewall() Firewall {
	if _, err := exec.LookPath("ufw"); err == nil {
		return UFW
	}
	if _, err := exec.LookPath("firewall-cmd"); err == nil {
		return Firewalld
	}
	return Unknown
}

// Fix is the command that opens port to subnet, in the firewall's dialect.
func Fix(fw Firewall, port int, subnet string) string {
	if subnet == "" {
		subnet = "your local network"
	}
	family := "ipv4"
	if strings.Contains(subnet, ":") {
		family = "ipv6"
	}
	switch fw {
	case UFW:
		return fmt.Sprintf("sudo ufw allow from %s to any port %d proto tcp comment 'jaggery'", subnet, port)
	case Firewalld:
		return fmt.Sprintf("sudo firewall-cmd --permanent --add-rich-rule='rule family=%s source address=%s port port=%d protocol=tcp accept' && sudo firewall-cmd --reload", family, subnet, port)
	default:
		return fmt.Sprintf("allow inbound TCP port %d from %s in this machine's firewall", port, subnet)
	}
}

// SubnetOf returns the local subnet containing addr, e.g. 192.0.2.0/24,
// from this machine's interfaces; empty if none holds it.
func SubnetOf(addr netip.Addr) string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			prefix, err := netip.ParsePrefix(ipnet.String())
			if err != nil || !prefix.Contains(addr) {
				continue
			}
			return prefix.Masked().String()
		}
	}
	return ""
}
