package io.github.kmpavloff.a2ademo.orchestrator.web;

import com.fasterxml.jackson.annotation.JsonInclude;
import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.config.AuthConfig;
import io.github.kmpavloff.a2ademo.orchestrator.store.AgentStore;
import org.springframework.http.HttpStatus;
import org.springframework.http.MediaType;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.DeleteMapping;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.PutMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RestController;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;

/**
 * Правка списка агентов из браузера (порт internal/webui/agentconfig.go).
 *
 * <p>Эндпоинты ничем не защищены — как и {@code /invoke}: это демо-стенд.
 * Значит, режим {@code --web} нельзя выставлять за пределы доверенной сети:
 * через этот API можно подменить адрес агента и увести туда весь разговор.
 */
@RestController
public class AgentConfigController {

    /**
     * Форма записи в ответе. Отдельный от входного тип, и это не дублирование
     * ради красоты: у него просто нет поля пароля, поэтому вернуть пароль наружу
     * нельзя даже по невнимательности.
     */
    @JsonInclude(JsonInclude.Include.ALWAYS)
    public static class AgentOut {
        public String id;
        public String name;
        public String url;
        public String cardPath;
        public String skill;
        public boolean verbatim;
        public String timeout;
        public String description;
        public AuthOut auth = new AuthOut();
        public boolean hidden;
        public String source;
        public List<String> envLocked = List.of();
        /**
         * Есть ли что сбрасывать: базовая версия в orchestrator.yaml, к которой
         * можно вернуться, И правка в overlay, которую для этого надо забыть.
         * Оба условия нужны. У агента, целиком заведённого через UI, нет
         * первого: «сброс» означал бы безвозвратное удаление, а это дело кнопки
         * «Удалить» с подтверждением. У нетронутого агента из файла нет второго —
         * он и так равен своей версии из конфига.
         */
        public boolean canReset;
    }

    @JsonInclude(JsonInclude.Include.ALWAYS)
    public static class AuthOut {
        public String type = "";
        public String username = "";
        public boolean hasPassword;
    }

    /**
     * То, что присылает форма. Пустой password означает «не менять»: прочитать
     * текущий браузер не может, и форма шлёт пустое поле каждый раз, когда
     * пароль не трогали.
     */
    public static class AgentIn {
        public String id = "";
        public String name = "";
        public String url = "";
        public String cardPath = "";
        public String skill = "";
        public boolean verbatim;
        public String timeout = "";
        public String description = "";
        public AuthIn auth = new AuthIn();

        AgentConfig toConfig() {
            return new AgentConfig(id, name, url, cardPath, skill, verbatim, timeout, description,
                    new AuthConfig(auth.type, auth.username, auth.password));
        }
    }

    public static class AuthIn {
        public String type = "";
        public String username = "";
        public String password = "";
    }

    private final AgentStore store;

    public AgentConfigController(AgentStore store) {
        this.store = store;
    }

    @GetMapping(value = "/api/agents/config", produces = MediaType.APPLICATION_JSON_VALUE)
    public List<AgentOut> list() {
        List<AgentOut> out = new ArrayList<>();
        for (AgentStore.Record r : store.records()) {
            out.add(toOut(r));
        }
        return out;
    }

    @PostMapping(value = "/api/agents/config", produces = MediaType.APPLICATION_JSON_VALUE)
    public ResponseEntity<Object> create(@RequestBody AgentIn in) {
        return result(HttpStatus.CREATED, () -> store.create(in.toConfig()));
    }

    @PutMapping(value = "/api/agents/config/{id}", produces = MediaType.APPLICATION_JSON_VALUE)
    public ResponseEntity<Object> update(@PathVariable String id, @RequestBody AgentIn in) {
        return result(HttpStatus.OK, () -> store.update(id, in.toConfig()));
    }

    @DeleteMapping(value = "/api/agents/config/{id}", produces = MediaType.APPLICATION_JSON_VALUE)
    public ResponseEntity<Object> delete(@PathVariable String id) {
        return result(HttpStatus.NO_CONTENT, () -> store.delete(id));
    }

    @PostMapping(value = "/api/agents/config/{id}/reset", produces = MediaType.APPLICATION_JSON_VALUE)
    public ResponseEntity<Object> reset(@PathVariable String id) {
        return result(HttpStatus.OK, () -> store.reset(id));
    }

    private static AgentOut toOut(AgentStore.Record r) {
        AgentConfig a = r.agent();
        AgentOut o = new AgentOut();
        o.id = a.id();
        o.name = a.name();
        o.url = a.url();
        o.cardPath = a.cardPath();
        o.skill = a.skill();
        o.verbatim = a.verbatim();
        o.timeout = a.timeout();
        o.description = a.description();
        o.auth.type = a.auth().type();
        o.auth.username = a.auth().username();
        o.auth.hasPassword = r.hasPassword();
        o.hidden = r.hidden();
        o.source = r.source();
        o.envLocked = r.envLocked();
        o.canReset = r.inFile() && r.source().equals(AgentStore.SOURCE_UI);
        return o;
    }

    /**
     * Переводит ошибку хранилища в код ответа: «нет такого» и «уже есть» — не то
     * же самое, что кривая запись, и форма показывает их по-разному.
     */
    private static ResponseEntity<Object> result(HttpStatus okStatus, Runnable mutation) {
        try {
            mutation.run();
        } catch (AgentStore.NotFoundException e) {
            return ResponseEntity.status(HttpStatus.NOT_FOUND).body(Map.of("error", e.getMessage()));
        } catch (AgentStore.ExistsException e) {
            return ResponseEntity.status(HttpStatus.CONFLICT).body(Map.of("error", e.getMessage()));
        } catch (RuntimeException e) {
            return ResponseEntity.badRequest().body(Map.of("error", String.valueOf(e.getMessage())));
        }
        if (okStatus == HttpStatus.NO_CONTENT) {
            return ResponseEntity.noContent().build();
        }
        return ResponseEntity.status(okStatus).body(Map.of("status", "ok"));
    }
}
