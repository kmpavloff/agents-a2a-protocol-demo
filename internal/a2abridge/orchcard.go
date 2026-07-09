package a2abridge

import (
	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/a2ui"
)

// OrchestratorCard is the AgentCard the orchestrator serves to browser A2UI
// clients. It advertises the A2UI A2A-extension so clients activate generative
// UI, and a JSONRPC interface at publicURL/invoke (matching the mux in main.go).
func OrchestratorCard(publicURL string) *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:               "orders-orchestrator",
		Description:        "Оркестратор поддержки: делегирует работу с заказами и отдаёт A2UI-виджеты.",
		Version:            "0.1.0",
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{
				URI:         a2ui.ExtensionURI,
				Description: "Отдаёт интерфейс через A2UI (generative UI).",
			}},
		},
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(publicURL+"/invoke", a2a.TransportProtocolJSONRPC),
		},
	}
}
