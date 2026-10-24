package dashboard

import (
	"regexp"
	"strings"
	"testing"
)

// Nothing on the page may leave the house: no scripts, styles or fonts from
// elsewhere, and the only fetches are the agent's own /v1/house endpoints.
func TestPageStaysInTheHouse(t *testing.T) {
	page := string(Index())

	for _, tag := range regexp.MustCompile(`<(script|link|img|iframe)\b[^>]*>`).FindAllString(page, -1) {
		// A data: URL (the favicon) is inline: it fetches nothing.
		for _, ref := range regexp.MustCompile(`(?:src|href)="([^"]*)"`).FindAllStringSubmatch(tag, -1) {
			if !strings.HasPrefix(ref[1], "data:") {
				t.Errorf("page loads an outside resource: %s", tag)
			}
		}
	}
	if strings.Contains(page, "@import") || strings.Contains(page, "url(") {
		t.Error("page CSS loads an outside resource")
	}
	for _, m := range regexp.MustCompile(`fetch\(\s*"([^"]*)"`).FindAllStringSubmatch(page, -1) {
		if !strings.HasPrefix(m[1], "/v1/house") {
			t.Errorf("page fetches %q; only /v1/house* is allowed", m[1])
		}
	}
	for _, u := range regexp.MustCompile(`https?://[^\s"'<>`+"`"+`]+`).FindAllString(page, -1) {
		if !strings.Contains(u, ".local") {
			t.Errorf("page names an address outside the house: %s", u)
		}
	}
}
