package webui

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/agentstore"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/config"
)

// agentOut — форма записи в ответе. Отдельный от входного тип, и это не
// дублирование ради красоты: у него просто нет поля пароля, поэтому вернуть
// пароль наружу нельзя даже по невнимательности.
type agentOut struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	URL         string   `json:"url"`
	CardPath    string   `json:"cardPath"`
	Skill       string   `json:"skill"`
	Verbatim    bool     `json:"verbatim"`
	Timeout     string   `json:"timeout"`
	Description string   `json:"description"`
	Auth        authOut  `json:"auth"`
	TLS         tlsJSON  `json:"tls"`
	Hidden      bool     `json:"hidden"`
	Source      string   `json:"source"`
	EnvLocked   []string `json:"envLocked"`
	// CanReset — есть ли что сбрасывать: базовая версия в orchestrator.yaml, к
	// которой можно вернуться, И правка в overlay, которую для этого надо
	// забыть. Оба условия нужны. У агента, целиком заведённого через UI, нет
	// первого: «сброс» означал бы безвозвратное удаление, а это дело кнопки
	// «Удалить» с подтверждением. У нетронутого агента из файла нет второго —
	// он и так равен своей версии из конфига, и сброс вернул бы «нет такой
	// записи», сообщение не по делу.
	CanReset bool `json:"canReset"`
}

type authOut struct {
	Type        string `json:"type"`
	Username    string `json:"username"`
	HasPassword bool   `json:"hasPassword"`
}

// tlsJSON — пути к PEM-файлам. Один тип на вход и выход: в нём нет секретов,
// только пути на машине оркестратора.
type tlsJSON struct {
	CertFile           string `json:"certFile"`
	KeyFile            string `json:"keyFile"`
	CAFile             string `json:"caFile"`
	InsecureSkipVerify bool   `json:"insecureSkipVerify"`
}

func (t tlsJSON) toConfig() config.TLSConfig {
	return config.TLSConfig{CertFile: t.CertFile, KeyFile: t.KeyFile, CAFile: t.CAFile,
		InsecureSkipVerify: t.InsecureSkipVerify}
}

func tlsOut(t config.TLSConfig) tlsJSON {
	return tlsJSON{CertFile: t.CertFile, KeyFile: t.KeyFile, CAFile: t.CAFile,
		InsecureSkipVerify: t.InsecureSkipVerify}
}

// agentIn — то, что присылает форма. Пустой password означает «не менять»:
// прочитать текущий браузер не может, и форма шлёт пустое поле каждый раз,
// когда пароль не трогали.
type agentIn struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	CardPath    string `json:"cardPath"`
	Skill       string `json:"skill"`
	Verbatim    bool   `json:"verbatim"`
	Timeout     string `json:"timeout"`
	Description string `json:"description"`
	Auth        struct {
		Type     string `json:"type"`
		Username string `json:"username"`
		Password string `json:"password"`
	} `json:"auth"`
	TLS tlsJSON `json:"tls"`
}

func (in agentIn) toConfig() config.AgentConfig {
	return config.AgentConfig{
		ID: in.ID, Name: in.Name, URL: in.URL, CardPath: in.CardPath,
		Skill: in.Skill, Verbatim: in.Verbatim, Timeout: in.Timeout,
		Description: in.Description,
		Auth: config.AuthConfig{
			Type: in.Auth.Type, Username: in.Auth.Username, Password: in.Auth.Password,
		},
		TLS: in.TLS.toConfig(),
	}
}

func toOut(r agentstore.Record) agentOut {
	locked := r.EnvLocked
	if locked == nil {
		locked = []string{} // фронтенд итерирует без проверок на null
	}
	return agentOut{
		ID: r.ID, Name: r.Name, URL: r.URL, CardPath: r.CardPath,
		Skill: r.Skill, Verbatim: r.Verbatim, Timeout: r.Timeout,
		Description: r.Description,
		Auth: authOut{
			Type: r.Auth.Type, Username: r.Auth.Username, HasPassword: r.HasPassword,
		},
		TLS:    tlsOut(r.TLS),
		Hidden: r.Hidden, Source: r.Source, EnvLocked: locked,
		CanReset: r.InFile && r.Source == agentstore.SourceUI,
	}
}

// RegisterAgentConfig вешает на mux редактирование списка агентов.
//
// Эндпоинты ничем не защищены — как и /invoke: это демо-стенд. Значит, режим
// --web нельзя выставлять за пределы доверенной сети: через этот API можно
// подменить адрес агента и увести туда весь разговор.
func RegisterAgentConfig(mux *http.ServeMux, s *agentstore.Store) {
	mux.HandleFunc("GET /api/agents/config", func(w http.ResponseWriter, r *http.Request) {
		recs := s.Records()
		out := make([]agentOut, 0, len(recs))
		for _, rec := range recs {
			out = append(out, toOut(rec))
		}
		writeJSON(w, http.StatusOK, out)
	})

	mux.HandleFunc("POST /api/agents/config", func(w http.ResponseWriter, r *http.Request) {
		in, ok := decodeAgent(w, r)
		if !ok {
			return
		}
		writeResult(w, http.StatusCreated, s.Create(in.toConfig()))
	})

	mux.HandleFunc("PUT /api/agents/config/{id}", func(w http.ResponseWriter, r *http.Request) {
		in, ok := decodeAgent(w, r)
		if !ok {
			return
		}
		writeResult(w, http.StatusOK, s.Update(r.PathValue("id"), in.toConfig()))
	})

	mux.HandleFunc("DELETE /api/agents/config/{id}", func(w http.ResponseWriter, r *http.Request) {
		writeResult(w, http.StatusNoContent, s.Delete(r.PathValue("id")))
	})

	mux.HandleFunc("POST /api/agents/config/{id}/reset", func(w http.ResponseWriter, r *http.Request) {
		writeResult(w, http.StatusOK, s.Reset(r.PathValue("id")))
	})
}

func decodeAgent(w http.ResponseWriter, r *http.Request) (agentIn, bool) {
	var in agentIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "неразбираемый JSON: " + err.Error()})
		return in, false
	}
	return in, true
}

// writeResult переводит ошибку хранилища в код ответа: «нет такого» и «уже
// есть» — не то же самое, что кривая запись, и форма показывает их по-разному.
func writeResult(w http.ResponseWriter, okStatus int, err error) {
	switch {
	case err == nil:
		if okStatus == http.StatusNoContent {
			w.WriteHeader(okStatus)
			return
		}
		writeJSON(w, okStatus, map[string]string{"status": "ok"})
	case errors.Is(err, agentstore.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, agentstore.ErrExists):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Заголовок статуса уже отправлен WriteHeader выше — исправить ответ
		// или сообщить о нём вызывающему уже нельзя, клиент получит оборванное
		// тело. Здесь намеренно ничего не залогировано: пакет webui не хранит
		// логгер, а это демо-стенд, где такой отказ (кодирование JSON из своих
		// же типов) практически недостижим.
		return
	}
}
