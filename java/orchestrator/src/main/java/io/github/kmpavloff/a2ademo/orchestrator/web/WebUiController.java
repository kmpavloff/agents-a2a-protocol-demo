package io.github.kmpavloff.a2ademo.orchestrator.web;

import org.springframework.core.io.FileSystemResource;
import org.springframework.core.io.Resource;
import org.springframework.http.MediaType;
import org.springframework.http.MediaTypeFactory;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;
import jakarta.servlet.http.HttpServletRequest;

import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;

/**
 * Serves the built A2UI browser frontend (port of internal/webui). Unlike the
 * Go binary, the jar does not embed the build — the frontend is looked up on
 * disk at startup: WEBUI_DIST env, then internal/webui/dist, then web/dist
 * (all relative to the working directory). Unknown paths fall back to
 * index.html so the single-page app can boot from any URL; when no build is
 * found a friendly "not built" page is served.
 */
@RestController
public class WebUiController {

    static final String NOT_BUILT_HTML = "<!doctype html><html lang=\"ru\"><head><meta charset=\"utf-8\">"
            + "<title>A2UI — фронтенд не собран</title></head>"
            + "<body style=\"font-family:system-ui;max-width:640px;margin:64px auto;padding:0 24px;color:#222\">"
            + "<h2>Фронтенд не собран</h2><p>Соберите его и перезапустите оркестратор:</p>"
            + "<pre style=\"background:#f0f1f3;padding:12px;border-radius:8px\">cd web &amp;&amp; yarn install &amp;&amp; yarn build</pre>"
            + "</body></html>";

    private final Path dist; // null when no build was found

    public WebUiController() {
        this(resolveDist());
    }

    WebUiController(Path dist) {
        this.dist = dist;
    }

    /** First candidate directory that contains an index.html, or null. */
    static Path resolveDist() {
        String env = System.getenv("WEBUI_DIST");
        String[] candidates = env != null && !env.isBlank()
                ? new String[]{env}
                : new String[]{"internal/webui/dist", "web/dist"};
        for (String c : candidates) {
            Path p = Path.of(c);
            if (Files.isRegularFile(p.resolve("index.html"))) {
                return p.toAbsolutePath().normalize();
            }
        }
        return null;
    }

    @GetMapping("/**")
    public ResponseEntity<Resource> serve(HttpServletRequest request) {
        if (dist == null) {
            return ResponseEntity.ok()
                    .contentType(new MediaType(MediaType.TEXT_HTML, StandardCharsets.UTF_8))
                    .body(new org.springframework.core.io.ByteArrayResource(
                            NOT_BUILT_HTML.getBytes(StandardCharsets.UTF_8)));
        }
        String rel = request.getRequestURI().replaceFirst("^/+", "");
        Path file = dist.resolve(rel).normalize();
        // Path-traversal guard + SPA fallback for unknown routes.
        if (rel.isEmpty() || !file.startsWith(dist) || !Files.isRegularFile(file)) {
            file = dist.resolve("index.html");
        }
        MediaType type = MediaTypeFactory.getMediaType(file.getFileName().toString())
                .orElse(MediaType.APPLICATION_OCTET_STREAM);
        return ResponseEntity.ok().contentType(type).body(new FileSystemResource(file));
    }
}
