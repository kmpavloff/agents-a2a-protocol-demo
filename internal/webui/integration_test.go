package webui_test

// Сквозной тест главного тракта правки агентов: HTTP-ручка → Store → подписка
// Registry.OnChange → живой Registry. Каждый стык уже проверен по отдельности
// (agentstore/store_test.go, a2abridge/registry_test.go, webui/agentconfig_test.go),
// а вот их склейку раньше проверяли только руками. Пакет отдельный
// (webui_test), чтобы тест жил рядом с обработчиками, но не создавал цикла
// импортов: webui уже знает про agentstore, а здесь ему добавляется ещё и
// a2abridge — оба пакета webui не импортируют.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/a2abridge"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/agentstore"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/config"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/webui"
)

func TestAgentConfigEditReachesLiveRegistry(t *testing.T) {
	base := []config.AgentConfig{
		{ID: "orders", Name: "Агент заказов", URL: "http://localhost:8081"},
	}
	store, err := agentstore.New(base, filepath.Join(t.TempDir(), "agents.local.yaml"))
	if err != nil {
		t.Fatalf("agentstore.New: %v", err)
	}

	trace := a2abridge.NewTracer(io.Discard, "")
	reg := a2abridge.NewRegistry(store.Agents(), trace)
	// Ровно так это подключено в cmd/orchestrator/main.go: подписчик реестра —
	// это то, что делает правку из UI видимой без перезапуска.
	store.OnChange(reg.Apply)

	oldRemote, ok := reg.Get("orders")
	if !ok {
		t.Fatal("agent not in fresh registry")
	}
	oldGen := reg.Generation()

	mux := http.NewServeMux()
	webui.RegisterAgentConfig(mux, store)

	body := `{"id":"orders","name":"Свой воркер","url":"http://127.0.0.1:9000"}`
	req := httptest.NewRequest(http.MethodPut, "/api/agents/config/orders", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status: %d, body: %s", rec.Code, rec.Body)
	}

	// 1. Store отразил правку.
	agents := store.Agents()
	if len(agents) != 1 || agents[0].URL != "http://127.0.0.1:9000" || agents[0].Name != "Свой воркер" {
		t.Fatalf("store не применил правку: %+v", agents)
	}

	// 2. Поколение реестра выросло — так исполнитель узнаёт, что кэш runner'ов
	// пора выбросить.
	if newGen := reg.Generation(); newGen <= oldGen {
		t.Errorf("Generation: было %d, стало %d — правка не докатилась до реестра", oldGen, newGen)
	}

	// 3. Get вернул НОВЫЙ *Remote: конфиг агента поменялся, и старое соединение
	// с прежним адресом переиспользовать нельзя.
	newRemote, ok := reg.Get("orders")
	if !ok {
		t.Fatal("agent исчез из реестра после правки")
	}
	if newRemote == oldRemote {
		t.Error("реестр отдал тот же *Remote — конфиг изменился, соединение должно быть пересоздано")
	}
}
