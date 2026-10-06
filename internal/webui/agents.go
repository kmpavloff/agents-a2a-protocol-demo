package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
)

// AgentsHandler отдаёт браузеру список агентов, между которыми можно
// переключаться. Обобщённый по типу элемента, чтобы webui не зависел от
// транспортного пакета — сюда передают готовую функцию-источник.
func AgentsHandler[T any](list func(context.Context) []T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agents := list(r.Context())
		if agents == nil {
			agents = []T{}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if err := json.NewEncoder(w).Encode(agents); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
}

// AgentCallsHandler отдаёт панели «A2A-протокол» обмены оркестратора с
// агентами за разговор: GET ?contextId=…&after=<seq>. Источник — функция, по
// той же причине, что и у AgentsHandler: webui не знает транспортного пакета.
func AgentCallsHandler[T any](since func(contextID string, after int64) []T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxID := r.URL.Query().Get("contextId")
		if ctxID == "" {
			http.Error(w, "contextId is required", http.StatusBadRequest)
			return
		}
		var after int64
		if s := r.URL.Query().Get("after"); s != "" {
			v, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				http.Error(w, "bad after: "+err.Error(), http.StatusBadRequest)
				return
			}
			after = v
		}
		calls := since(ctxID, after)
		if calls == nil {
			calls = []T{}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if err := json.NewEncoder(w).Encode(calls); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
}
