package webui

import (
	"context"
	"encoding/json"
	"net/http"
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
