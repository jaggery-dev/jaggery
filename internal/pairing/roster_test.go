package pairing

import (
	"encoding/base64"
	"errors"
	"testing"
	"time"
)

func testRoster(issuer machine, members ...machine) Roster {
	r := Roster{Issuer: issuer.id, IssuedAt: time.Now().UTC()}
	for _, m := range members {
		r.Members = append(r.Members, Member{MachineID: m.id, Name: "m", PublicKey: m.pub, Port: 7373})
	}
	return r
}

func TestRosterRoundTrip(t *testing.T) {
	pi, desktop, fourth := newMachine(t), newMachine(t), newMachine(t)
	signed, err := SignRoster(pi.identity(), testRoster(pi, desktop, fourth))
	if err != nil {
		t.Fatal(err)
	}
	got, err := OpenRoster(signed, pi.pub)
	if err != nil {
		t.Fatal(err)
	}
	if got.Issuer != pi.id || len(got.Members) != 2 || got.Members[1].MachineID != fourth.id {
		t.Fatalf("roster came back as %+v", got)
	}
}

func TestRosterRejectsTamperingAndImpostors(t *testing.T) {
	pi, desktop, mallory := newMachine(t), newMachine(t), newMachine(t)
	signed, err := SignRoster(pi.identity(), testRoster(pi, desktop))
	if err != nil {
		t.Fatal(err)
	}

	tampered := signed
	tampered.Body = append([]byte(nil), signed.Body...)
	tampered.Body[len(tampered.Body)-2] ^= 1
	if _, err := OpenRoster(tampered, pi.pub); !errors.Is(err, ErrBadRoster) {
		t.Errorf("tampered body: got %v, want ErrBadRoster", err)
	}

	// Signed by the Pi, presented as someone else's.
	if _, err := OpenRoster(signed, mallory.pub); !errors.Is(err, ErrBadRoster) {
		t.Errorf("wrong sender: got %v, want ErrBadRoster", err)
	}

	// Mallory signs a roster in the Pi's name.
	forged, _ := SignRoster(mallory.identity(), testRoster(pi, mallory))
	if _, err := OpenRoster(forged, mallory.pub); !errors.Is(err, ErrBadRoster) {
		t.Errorf("roster naming another issuer: got %v, want ErrBadRoster", err)
	}
}

// A member's key must derive its id, or a voucher could attach any key to a
// trusted machine's id.
func TestRosterRejectsMemberWithForeignKey(t *testing.T) {
	pi, desktop, mallory := newMachine(t), newMachine(t), newMachine(t)
	r := testRoster(pi, desktop)
	r.Members[0].PublicKey = mallory.pub
	signed, _ := SignRoster(pi.identity(), r)
	if _, err := OpenRoster(signed, pi.pub); !errors.Is(err, ErrBadMemberKey) {
		t.Fatalf("got %v, want ErrBadMemberKey", err)
	}
}

// The prove endpoint signs for strangers, so it must not sign a roster.
func TestAnswerWontSignARoster(t *testing.T) {
	pi := newMachine(t)
	body := rosterMessage([]byte(`{"issuer":"` + pi.id + `","members":[]}`))
	c := Challenge{Nonce: base64.StdEncoding.EncodeToString(body)}
	if _, err := Answer(pi.identity(), c); err == nil {
		t.Fatal("Answer signed a roster-shaped message")
	}
}
