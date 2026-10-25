package licences

import (
	"strings"
	"testing"
)

func TestTextNamesJaggeryGoAndModules(t *testing.T) {
	for _, want := range []string{
		"Copyright 2026 The Jaggery Authors",
		"Apache License",
		"Go, its standard library and runtime",
		"github.com/miekg/dns",
		"golang.org/x/net",
	} {
		if !strings.Contains(Text, want) {
			t.Errorf("licences.txt doesn't contain %q: run sh scripts/gen-licences.sh", want)
		}
	}
}
