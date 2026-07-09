package a2abridge

import (
	"testing"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/a2ui"
)

func TestOrchestratorCardAdvertisesA2UI(t *testing.T) {
	card := OrchestratorCard("http://localhost:8080")
	var found bool
	for _, e := range card.Capabilities.Extensions {
		if e.URI == a2ui.ExtensionURI {
			found = true
		}
	}
	if !found {
		t.Errorf("card must advertise the A2UI extension %q; got %#v", a2ui.ExtensionURI, card.Capabilities.Extensions)
	}
	if len(card.SupportedInterfaces) == 0 {
		t.Fatal("card must expose a JSONRPC interface")
	}
}
