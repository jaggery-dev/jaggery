package apps

import (
	"errors"
	"fmt"
	"strings"
)

// App names (track W, tech-specs/app-names.md): the owner gives an app on
// this machine a short name, and the house reaches it at <name>.local. The
// name is kept in apps.json beside the entries and travels with the app in
// the inventory, as App.LocalName.

// Name gives the app called App (as the page shows it) the name Name.
type Name struct {
	App  string `json:"app"`
	Name string `json:"name"`
}

// Reserved are Jaggery's own names.
var Reserved = []string{"jaggery", "ai"}

// ErrNoName: the app has no name to remove.
var ErrNoName = errors.New("that app has no name on this machine")

// CheckName reports what's wrong with n as an app name: it must be one DNS
// label, and not one of Jaggery's own names.
func CheckName(n string) error {
	if n == "" || len(n) > 63 {
		return fmt.Errorf("a name is 1 to 63 characters, like chat")
	}
	for _, r := range n {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return fmt.Errorf("%q can't be a name: use lower-case letters, digits and hyphens, like chat or photos", n)
		}
	}
	if n[0] == '-' || n[len(n)-1] == '-' {
		return fmt.Errorf("%q can't be a name: it can't start or end with a hyphen", n)
	}
	for _, r := range Reserved {
		if n == r {
			return fmt.Errorf("%s.local is Jaggery's own; choose another name", n)
		}
	}
	return nil
}

// NormalName turns what a person typed (" Chat.local") into a name ("chat").
func NormalName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.TrimSuffix(s, ".local")
}

// LoadNames reads this machine's app names, sorted by name.
func LoadNames(dir string) ([]Name, error) {
	f, err := loadFile(dir)
	return f.Names, err
}

// SetName gives app the name n, replacing any name it had: one name per app.
// Checking n against the rest of the house is the caller's job.
func SetName(dir, app, n string) error {
	app = strings.TrimSpace(app)
	if err := CheckName(n); err != nil {
		return err
	}
	f, err := loadFile(dir)
	if err != nil {
		return err
	}
	out := []Name{{App: app, Name: n}}
	for _, e := range f.Names {
		if e.Name == n && !strings.EqualFold(e.App, app) {
			return fmt.Errorf("%s is already the name of %s on this machine; remove it first: jag apps name %q --remove", n, e.App, e.App)
		}
		if !strings.EqualFold(e.App, app) {
			out = append(out, e)
		}
	}
	f.Names = out
	return saveFile(dir, f)
}

// RemoveName takes app's name away.
func RemoveName(dir, app string) error {
	f, err := loadFile(dir)
	if err != nil {
		return err
	}
	var out []Name
	for _, e := range f.Names {
		if !strings.EqualFold(e.App, strings.TrimSpace(app)) {
			out = append(out, e)
		}
	}
	if len(out) == len(f.Names) {
		return ErrNoName
	}
	f.Names = out
	return saveFile(dir, f)
}

// ApplyNames sets LocalName on each named app. Only apps with a port can be
// named: a link points somewhere Jaggery doesn't run. A name that fails
// CheckName (a hand-edited file) is skipped.
func ApplyNames(list []App, names []Name) []App {
	out := append([]App(nil), list...)
	for _, n := range names {
		if CheckName(n.Name) != nil {
			continue
		}
		for i := range out {
			if out[i].URL == "" && out[i].Port != 0 && strings.EqualFold(out[i].Name, n.App) {
				out[i].LocalName = n.Name
				break
			}
		}
	}
	return out
}
