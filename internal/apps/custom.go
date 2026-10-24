package apps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Apps the owner adds by hand (jag apps add), for what the catalog doesn't
// know: an app on this machine (by port), a link to something that isn't a
// Jaggery machine (the router, a printer), or a better description for a
// detected app. Kept per machine and sent in its inventory, so every page in
// the house shows them.

// CustomFile holds this machine's own entries, in the state directory.
const CustomFile = "apps.json"

// Custom is one entry. Exactly one of three kinds:
//   - Port set: an app on this machine, listed while the port answers on
//     the LAN address. If a detected app is on that port, the entry renames
//     it instead.
//   - URL set: a link, listed as it is.
//   - Neither: a new description for the detected app with this name.
type Custom struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Port        int    `json:"port,omitempty"`
	Path        string `json:"path,omitempty"`
	URL         string `json:"url,omitempty"`
}

type customFile struct {
	Apps []Custom `json:"apps"`
	// Names are the short names the owner gave apps (jag apps name).
	Names []Name `json:"names,omitempty"`
}

// ErrNoCustom: no entry by that name.
var ErrNoCustom = errors.New("no app by that name was added on this machine")

const (
	maxName = 40
	maxDesc = 80
)

// Check reports what's wrong with c, in words for the person who typed it.
func (c Custom) Check() error {
	if strings.TrimSpace(c.Name) == "" {
		return errors.New("an app needs a name")
	}
	if len([]rune(c.Name)) > maxName || hasControl(c.Name) {
		return fmt.Errorf("the name must be at most %d characters, on one line", maxName)
	}
	if len([]rune(c.Description)) > maxDesc || hasControl(c.Description) {
		return fmt.Errorf("the description must be at most %d characters, on one line", maxDesc)
	}
	if c.Port != 0 && c.URL != "" {
		return errors.New("give --port for an app on this machine or --url for a link, not both")
	}
	if c.Port < 0 || c.Port > 65535 {
		return fmt.Errorf("port %d isn't a port number (1 to 65535)", c.Port)
	}
	if c.Path != "" {
		if c.Port == 0 {
			return errors.New("--path goes with --port; put the path in the --url instead")
		}
		if !strings.HasPrefix(c.Path, "/") || hasControl(c.Path) {
			return fmt.Errorf("the path must start with /, like /web")
		}
	}
	if c.URL != "" {
		return checkURL(c.URL)
	}
	return nil
}

// checkURL allows only http and https links with a host: anything else
// (javascript:, data:) on the page would be a way to run code in it.
func checkURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || hasControl(s) {
		return fmt.Errorf("%q isn't a web address; give one starting http:// or https://", s)
	}
	return nil
}

func hasControl(s string) bool {
	return strings.IndexFunc(s, unicode.IsControl) >= 0
}

// LoadCustom reads this machine's entries, sorted by name; none when the
// file is missing.
func LoadCustom(dir string) ([]Custom, error) {
	f, err := loadFile(dir)
	return f.Apps, err
}

func loadFile(dir string) (customFile, error) {
	var f customFile
	b, err := os.ReadFile(filepath.Join(dir, CustomFile))
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return customFile{}, fmt.Errorf("reading %s: %w", CustomFile, err)
	}
	sort.Slice(f.Apps, func(i, j int) bool { return strings.ToLower(f.Apps[i].Name) < strings.ToLower(f.Apps[j].Name) })
	sort.Slice(f.Names, func(i, j int) bool { return f.Names[i].Name < f.Names[j].Name })
	return f, nil
}

// AddCustom saves c, replacing an entry with the same name (any case).
func AddCustom(dir string, c Custom) error {
	c.Name = strings.TrimSpace(c.Name)
	c.Description = strings.TrimSpace(c.Description)
	if err := c.Check(); err != nil {
		return err
	}
	f, err := loadFile(dir)
	if err != nil {
		return err
	}
	out := []Custom{c}
	for _, e := range f.Apps {
		if !strings.EqualFold(e.Name, c.Name) {
			out = append(out, e)
		}
	}
	f.Apps = out
	return saveFile(dir, f)
}

// RemoveCustom deletes the entry with this name (any case).
func RemoveCustom(dir, name string) error {
	f, err := loadFile(dir)
	if err != nil {
		return err
	}
	var out []Custom
	for _, e := range f.Apps {
		if !strings.EqualFold(e.Name, strings.TrimSpace(name)) {
			out = append(out, e)
		}
	}
	if len(out) == len(f.Apps) {
		return ErrNoCustom
	}
	f.Apps = out
	return saveFile(dir, f)
}

// saveFile writes the file whole, so changing the entries keeps the names
// and the other way round.
func saveFile(dir string, f customFile) error {
	if f.Apps == nil {
		f.Apps = []Custom{}
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, CustomFile+".tmp")
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, CustomFile))
}

// Merge applies the owner's entries to the detected apps. answers says
// whether a port is open on the LAN address; nil means none is.
func Merge(detected []App, custom []Custom, answers func(port int) bool) []App {
	out := append([]App(nil), detected...)
	for _, c := range custom {
		if c.Check() != nil {
			continue // a hand-edited file; skip the bad entry, keep the rest
		}
		switch {
		case c.URL != "":
			out = append(out, App{Name: c.Name, Description: c.Description, URL: c.URL, Custom: true})
		case c.Port != 0:
			if i := indexByPort(out, c.Port); i >= 0 {
				out[i] = relabel(out[i], c)
				if c.Path != "" {
					out[i].Path = c.Path
				}
				continue
			}
			if answers != nil && answers(c.Port) {
				out = append(out, App{Name: c.Name, Description: c.Description, Port: c.Port, Path: c.Path, Custom: true})
			}
		default:
			for i := range out {
				if !out[i].Custom && strings.EqualFold(out[i].Name, c.Name) {
					out[i] = relabel(out[i], c)
				}
			}
		}
	}
	return out
}

func relabel(a App, c Custom) App {
	a.Name = c.Name
	if c.Description != "" {
		a.Description = c.Description
	}
	a.Custom = true
	return a
}

func indexByPort(list []App, port int) int {
	for i, a := range list {
		if a.URL == "" && a.Port == port {
			return i
		}
	}
	return -1
}

// PortAnswers reports whether something accepts TCP connections on
// addr:port. Any protocol counts, so an HTTPS-only app is listed too.
func PortAnswers(ctx context.Context, addr netip.Addr, port int) bool {
	d := net.Dialer{Timeout: time.Second}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(addr.String(), strconv.Itoa(port)))
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
