package agentstore

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/config"
	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/testcerts"
)

func base() []config.AgentConfig {
	return []config.AgentConfig{
		{ID: "orders", Name: "Агент заказов", URL: "http://localhost:8081"},
		{ID: "ouroboros", Name: "Ouroboros", URL: "http://192.168.1.68:18800"},
	}
}

// Перекрытие идёт целой записью и не двигает агента по списку: порядок задаёт
// пункты селектора, и агент, которому поменяли адрес, не должен прыгать в конец.
func TestMergeOverridesInPlace(t *testing.T) {
	got := merge(base(), []Override{
		{AgentConfig: config.AgentConfig{ID: "orders", Name: "Мой воркер", URL: "http://127.0.0.1:9000"}},
	})
	if len(got) != 2 {
		t.Fatalf("agents: got %d, want 2", len(got))
	}
	if got[0].ID != "orders" || got[0].URL != "http://127.0.0.1:9000" || got[0].Name != "Мой воркер" {
		t.Errorf("перекрытая запись: %+v", got[0])
	}
	if got[1].ID != "ouroboros" || got[1].URL != "http://192.168.1.68:18800" {
		t.Errorf("нетронутая запись изменилась: %+v", got[1])
	}
}

// Поля, которых нет в overlay-записи, не подтягиваются из базовой: запись
// перекрывает агента целиком.
func TestMergeOverrideReplacesWholeRecord(t *testing.T) {
	got := merge(base(), []Override{
		{AgentConfig: config.AgentConfig{ID: "ouroboros", URL: "http://192.168.1.68:18800"}},
	})
	if got[1].Name != "" {
		t.Errorf("имя должно исчезнуть вместе с перекрытой записью, got %q", got[1].Name)
	}
}

func TestMergeHiddenDropsAgent(t *testing.T) {
	got := merge(base(), []Override{
		{AgentConfig: config.AgentConfig{ID: "orders"}, Hidden: true},
	})
	if len(got) != 1 || got[0].ID != "ouroboros" {
		t.Fatalf("скрытый агент не убран: %+v", got)
	}
}

func TestMergeAppendsUnknownIDs(t *testing.T) {
	got := merge(base(), []Override{
		{AgentConfig: config.AgentConfig{ID: "shop", URL: "http://localhost:9100"}},
		{AgentConfig: config.AgentConfig{ID: "billing", URL: "http://localhost:9200"}},
	})
	if len(got) != 4 {
		t.Fatalf("agents: got %d, want 4", len(got))
	}
	if got[2].ID != "shop" || got[3].ID != "billing" {
		t.Errorf("новые агенты должны идти в конец в порядке overlay: %+v", got)
	}
}

// Пустой overlay ничего не меняет: без файла демо работает ровно как раньше.
func TestMergeWithoutOverrides(t *testing.T) {
	got := merge(base(), nil)
	if len(got) != 2 || got[0].ID != "orders" || got[1].ID != "ouroboros" {
		t.Errorf("базовый список изменился: %+v", got)
	}
}

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "agents.local.yaml")
	s, err := New(base(), p)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, p
}

func TestStoreCreateAppendsAndPersists(t *testing.T) {
	s, p := newStore(t)
	if err := s.Create(config.AgentConfig{ID: "shop", URL: "http://localhost:9100"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	agents := s.Agents()
	if len(agents) != 3 || agents[2].ID != "shop" {
		t.Fatalf("agents: %+v", agents)
	}
	// Пережило перезапуск: новый Store с тем же файлом видит агента.
	again, err := New(base(), p)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if len(again.Agents()) != 3 {
		t.Errorf("после перечитывания: %+v", again.Agents())
	}
}

func TestStoreCreateRejectsDuplicateAndInvalid(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Create(config.AgentConfig{ID: "orders", URL: "http://x"}); !errors.Is(err, ErrExists) {
		t.Errorf("дубликат id: got %v, want ErrExists", err)
	}
	if err := s.Create(config.AgentConfig{ID: "БОЛЬШИЕ", URL: "http://x"}); err == nil {
		t.Error("невалидный id должен быть отвергнут")
	}
	// Отвергнутая запись не должна осесть в состоянии.
	if len(s.Agents()) != 2 {
		t.Errorf("список изменился после неудачных Create: %+v", s.Agents())
	}
}

func TestStoreUpdateOverridesFileAgent(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Update("orders", config.AgentConfig{ID: "orders", Name: "Свой", URL: "http://127.0.0.1:9000"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	agents := s.Agents()
	if agents[0].URL != "http://127.0.0.1:9000" || agents[0].Name != "Свой" {
		t.Errorf("правка не применилась: %+v", agents[0])
	}
	recs := s.Records()
	if recs[0].Source != "ui" {
		t.Errorf("источник: got %q, want ui", recs[0].Source)
	}
	if !recs[0].InFile {
		t.Error("у файлового агента с overlay-правкой InFile должен остаться true")
	}
}

// Пустой пароль в запросе означает «не менять»: наружу мы его не отдаём, и
// форма присылает пустое поле каждый раз, когда пароль не трогали.
func TestStoreUpdateKeepsPasswordWhenEmpty(t *testing.T) {
	s, _ := newStore(t)
	withPass := config.AgentConfig{
		ID: "ouroboros", URL: "http://192.168.1.68:18800",
		Auth: config.AuthConfig{Type: "basic", Username: "ouroboros", Password: "секрет"},
	}
	if err := s.Update("ouroboros", withPass); err != nil {
		t.Fatalf("Update: %v", err)
	}
	noPass := withPass
	noPass.Auth.Password = ""
	noPass.Name = "Новое имя"
	if err := s.Update("ouroboros", noPass); err != nil {
		t.Fatalf("Update: %v", err)
	}
	agents := s.Agents()
	if agents[1].Auth.Password != "секрет" {
		t.Errorf("пароль потерян: %q", agents[1].Auth.Password)
	}
	if agents[1].Name != "Новое имя" {
		t.Errorf("остальные поля не применились: %+v", agents[1])
	}
}

// Пароль и адрес, перекрытые окружением, не должны осесть в overlay-файле:
// иначе секрет из переменной окружения уходит на диск открытым текстом, а
// адрес конкретного контейнера замерзает в конфиге стенда.
func TestStoreUpdateDoesNotPersistEnvOverrides(t *testing.T) {
	t.Setenv("A2A_AGENT_OUROBOROS_URL", "http://from-env:18800")
	t.Setenv("A2A_AGENT_OUROBOROS_PASSWORD", "env-секрет")
	s, p := newStore(t)

	// Форма показывает пользователю действующее (env-) значение — Records его
	// и присылает обратно нетронутым, если пользователь его не трогал.
	recs := s.Records()
	var rec Record
	for _, r := range recs {
		if r.ID == "ouroboros" {
			rec = r
		}
	}
	if rec.URL != "http://from-env:18800" || !rec.HasPassword {
		t.Fatalf("Records должен показывать env-значения: %+v", rec)
	}

	// Пользователь правит только имя; адрес приходит env-значением (как его
	// показала форма), пароль — пустым (как его прислала бы форма, раз
	// показать реальный пароль нельзя).
	edited := config.AgentConfig{
		ID: "ouroboros", Name: "Новое имя", URL: rec.URL,
	}
	if err := s.Update("ouroboros", edited); err != nil {
		t.Fatalf("Update: %v", err)
	}

	over, err := loadOverlay(p)
	if err != nil {
		t.Fatalf("loadOverlay: %v", err)
	}
	i := indexOver(over, "ouroboros")
	if i < 0 {
		t.Fatal("overlay-запись не создана")
	}
	if over[i].URL == "http://from-env:18800" {
		t.Errorf("env-адрес попал в overlay-файл: %q", over[i].URL)
	}
	if over[i].URL != "http://192.168.1.68:18800" {
		t.Errorf("в overlay должен остаться прежний (базовый) адрес, а не env: %q", over[i].URL)
	}
	if over[i].Auth.Password != "" {
		t.Errorf("env-пароль попал в overlay-файл: %q", over[i].Auth.Password)
	}

	// Действующий список при этом всё равно живёт по env — слияние остаётся
	// последним словом окружения.
	agents := s.Agents()
	if agents[1].URL != "http://from-env:18800" {
		t.Errorf("env должен оставаться последним словом при слиянии: %+v", agents[1])
	}
	if agents[1].Name != "Новое имя" {
		t.Errorf("остальные поля должны были примениться: %+v", agents[1])
	}
}

func TestStoreUpdateUnknownAgent(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Update("нет-такого", config.AgentConfig{ID: "нет-такого", URL: "http://x"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

// Файлового агента удалить из YAML мы не можем — помечаем скрытым. В списке
// настроек он остаётся, иначе вернуть его можно было бы только правкой файла.
func TestStoreDeleteHidesFileAgent(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Delete("orders"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if agents := s.Agents(); len(agents) != 1 || agents[0].ID != "ouroboros" {
		t.Fatalf("agents: %+v", agents)
	}
	recs := s.Records()
	if len(recs) != 2 || recs[0].ID != "orders" || !recs[0].Hidden {
		t.Fatalf("скрытый агент должен остаться в Records: %+v", recs)
	}
}

// Агент, заведённый через UI, удаляется насовсем: скрывать нечего.
func TestStoreDeleteRemovesUIAgent(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Create(config.AgentConfig{ID: "shop", URL: "http://localhost:9100"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("shop"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	for _, r := range s.Records() {
		if r.ID == "shop" {
			t.Errorf("UI-агент должен исчезнуть целиком: %+v", r)
		}
	}
}

func TestStoreResetRestoresFileVersion(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Update("orders", config.AgentConfig{ID: "orders", URL: "http://127.0.0.1:9000"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("ouroboros"); err != nil {
		t.Fatal(err)
	}
	if err := s.Reset("orders"); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if err := s.Reset("ouroboros"); err != nil {
		t.Fatalf("Reset скрытого: %v", err)
	}
	agents := s.Agents()
	if len(agents) != 2 || agents[0].URL != "http://localhost:8081" {
		t.Errorf("сброс не вернул версию из файла: %+v", agents)
	}
	if s.Records()[0].Source != "file" {
		t.Errorf("источник после сброса: %q", s.Records()[0].Source)
	}
	if err := s.Reset("orders"); !errors.Is(err, ErrNotFound) {
		t.Errorf("сброс без overlay-записи: got %v, want ErrNotFound", err)
	}
}

// Агент, целиком заведённый через UI, базовой версии не имеет — «сброс» для
// него не может ничего вернуть и обязан отказать, а не молча стереть агента.
func TestStoreResetRejectsUIOnlyAgent(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Create(config.AgentConfig{ID: "shop", URL: "http://localhost:9100"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Reset("shop"); !errors.Is(err, ErrNotFound) {
		t.Errorf("сброс UI-агента: got %v, want ErrNotFound", err)
	}
	// Агент должен остаться на месте — «сброс» не должен был его стереть.
	found := false
	for _, a := range s.Agents() {
		if a.ID == "shop" {
			found = true
		}
	}
	if !found {
		t.Error("UI-агент исчез после неудавшегося Reset")
	}
	for _, r := range s.Records() {
		if r.ID == "shop" && r.InFile {
			t.Error("у UI-агента не должно быть InFile")
		}
	}
}

// Правка адреса, заданного переменной окружения, ничего не изменит — UI обязан
// об этом сказать, иначе пользователь чинит несуществующую поломку.
func TestStoreRecordsMarksEnvLocked(t *testing.T) {
	t.Setenv("A2A_AGENT_ORDERS_URL", "http://from-env:8081")
	s, _ := newStore(t)
	recs := s.Records()
	if recs[0].URL != "http://from-env:8081" {
		t.Errorf("показан адрес из файла, а не действующий: %q", recs[0].URL)
	}
	if len(recs[0].EnvLocked) != 1 || recs[0].EnvLocked[0] != "url" {
		t.Errorf("envLocked: %+v", recs[0].EnvLocked)
	}
}

func TestStoreRecordsReportPasswordWithoutRevealing(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Update("ouroboros", config.AgentConfig{
		ID: "ouroboros", URL: "http://192.168.1.68:18800",
		Auth: config.AuthConfig{Type: "basic", Username: "u", Password: "секрет"},
	}); err != nil {
		t.Fatal(err)
	}
	r := s.Records()[1]
	if !r.HasPassword {
		t.Error("HasPassword должен быть true")
	}
	if r.Auth.Password != "" {
		t.Errorf("Records не должны нести пароль: %q", r.Auth.Password)
	}
}

func TestStoreNotifiesOnChange(t *testing.T) {
	s, _ := newStore(t)
	var got [][]config.AgentConfig
	s.OnChange(func(agents []config.AgentConfig) { got = append(got, agents) })
	if err := s.Create(config.AgentConfig{ID: "shop", URL: "http://localhost:9100"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(config.AgentConfig{ID: "orders", URL: "http://x"}); err == nil {
		t.Fatal("ожидался отказ на дубликате")
	}
	if len(got) != 1 {
		t.Fatalf("подписчик вызван %d раз(а), want 1 — только на удачных правках", len(got))
	}
	if len(got[0]) != 3 {
		t.Errorf("подписчику приехал не тот список: %+v", got[0])
	}
}

// Пути к сертификатам живут как адрес: env-значение видно в форме и помечено
// как заблокированное, но в overlay ложится прежний путь, а не env-овый.
func TestStoreTLSPathsFromEnvStayOffDisk(t *testing.T) {
	fileCerts, envCerts := testcerts.New(t), testcerts.New(t)
	t.Setenv("A2A_AGENT_OUROBOROS_TLS_CERT", envCerts.ClientCert)
	t.Setenv("A2A_AGENT_OUROBOROS_TLS_KEY", envCerts.ClientKey)
	fileTLS := config.TLSConfig{CertFile: fileCerts.ClientCert, KeyFile: fileCerts.ClientKey, CAFile: fileCerts.CAFile}
	b := base()
	b[1].URL, b[1].TLS = "https://192.168.1.68:18800", fileTLS
	p := filepath.Join(t.TempDir(), "agents.local.yaml")
	s, err := New(b, p)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := s.Records()[1]
	if rec.TLS.CertFile != envCerts.ClientCert || rec.TLS.CAFile != fileCerts.CAFile {
		t.Errorf("Records должен показывать действующие пути: %+v", rec.TLS)
	}
	if !slices.Equal(rec.EnvLocked, []string{"tlsCert", "tlsKey"}) {
		t.Errorf("envLocked: %v", rec.EnvLocked)
	}

	// Форма вернула показанное как есть, правка — только имени.
	edited := rec.AgentConfig
	edited.Name = "Новое имя"
	if err := s.Update("ouroboros", edited); err != nil {
		t.Fatalf("Update: %v", err)
	}
	over, err := loadOverlay(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := over[indexOver(over, "ouroboros")].TLS; got != fileTLS {
		t.Errorf("в overlay должны остаться прежние пути, а не env: %+v", got)
	}
	if got := s.Agents()[1].TLS.CertFile; got != envCerts.ClientCert {
		t.Errorf("действующий список живёт по env: %q", got)
	}
}
