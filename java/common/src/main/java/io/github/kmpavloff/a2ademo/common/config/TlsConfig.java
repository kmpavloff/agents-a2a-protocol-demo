package io.github.kmpavloff.a2ademo.common.config;

import java.io.ByteArrayInputStream;
import java.io.IOException;
import java.net.Socket;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.GeneralSecurityException;
import java.security.KeyFactory;
import java.security.KeyStore;
import java.security.PrivateKey;
import java.security.Signature;
import java.security.cert.Certificate;
import java.security.cert.CertificateFactory;
import java.security.cert.X509Certificate;
import java.security.spec.PKCS8EncodedKeySpec;
import java.util.ArrayList;
import java.util.Base64;
import java.util.Collection;
import java.util.List;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import javax.net.ssl.KeyManager;
import javax.net.ssl.KeyManagerFactory;
import javax.net.ssl.SSLContext;
import javax.net.ssl.SSLEngine;
import javax.net.ssl.TrustManager;
import javax.net.ssl.TrustManagerFactory;
import javax.net.ssl.X509ExtendedTrustManager;

/**
 * Клиентский сертификат (mTLS) и доверие к серверу (порт config.TLSConfig).
 * Хранятся пути к PEM-файлам на машине оркестратора, а не содержимое:
 * приватный ключ не должен ходить через HTTP API настроек и оседать в overlay.
 *
 * <p>Живёт отдельно от {@link AuthConfig}: это свойство транспорта, и оно
 * сочетается с Basic, а не заменяет его.
 *
 * @param certFile клиентский сертификат, цепочка допустима
 * @param keyFile  его приватный ключ — только PKCS#8 ({@code BEGIN PRIVATE KEY}):
 *                 в JDK 21 нет разбора PKCS#1/SEC1, а тянуть ради него
 *                 BouncyCastle незачем
 * @param caFile   корни для проверки сервера; пусто → системные
 * @param insecureSkipVerify не проверять сертификат сервера вовсе — только для
 *                           стенда с самоподписанным сертификатом без CA
 */
public record TlsConfig(String certFile, String keyFile, String caFile, boolean insecureSkipVerify) {

    public static final TlsConfig NONE = new TlsConfig("", "", "", false);

    private static final Pattern PEM_KEY = Pattern.compile(
            "-----BEGIN ([A-Z ]*PRIVATE KEY)-----([A-Za-z0-9+/=\\s]+)-----END \\1-----");

    public TlsConfig {
        certFile = certFile == null ? "" : certFile;
        keyFile = keyFile == null ? "" : keyFile;
        caFile = caFile == null ? "" : caFile;
    }

    /** Задано ли хоть что-то из TLS-настроек. */
    public boolean enabled() {
        return !equals(NONE);
    }

    /**
     * Читает файлы и собирает {@link SSLContext}. Зовётся и при валидации:
     * битый путь должен ронять загрузку конфига или сохранение формы, а не
     * всплывать на первом запросе к агенту.
     *
     * @throws IllegalArgumentException с объяснением, что не так с файлами
     */
    public SSLContext sslContext() {
        if (certFile.isEmpty() != keyFile.isEmpty()) {
            throw new IllegalArgumentException("tls: cert_file and key_file must be set together");
        }
        try {
            KeyManager[] km = certFile.isEmpty() ? null : keyManagers();
            TrustManager[] tm = insecureSkipVerify ? new TrustManager[] {new TrustAll()}
                    : caFile.isEmpty() ? null : trustManagers();
            SSLContext ctx = SSLContext.getInstance("TLS");
            ctx.init(km, tm, null);
            return ctx;
        } catch (GeneralSecurityException e) {
            throw new IllegalArgumentException("tls: " + e.getMessage(), e);
        }
    }

    private KeyManager[] keyManagers() throws GeneralSecurityException {
        List<X509Certificate> chain = certificates(certFile, "cert_file");
        PrivateKey key = privateKey();
        // Go-шный tls.LoadX509KeyPair сверяет ключ с сертификатом; KeyStore —
        // нет, и несовпадение всплыло бы лишь невнятным обрывом рукопожатия.
        if (!matches(key, chain.get(0))) {
            throw new IllegalArgumentException("tls: private key does not match certificate " + certFile);
        }
        KeyStore ks = KeyStore.getInstance("PKCS12");
        try {
            ks.load(null, null);
        } catch (IOException e) {
            throw new GeneralSecurityException(e);
        }
        ks.setKeyEntry("client", key, new char[0], chain.toArray(new Certificate[0]));
        KeyManagerFactory kmf = KeyManagerFactory.getInstance(KeyManagerFactory.getDefaultAlgorithm());
        kmf.init(ks, new char[0]);
        return kmf.getKeyManagers();
    }

    private TrustManager[] trustManagers() throws GeneralSecurityException {
        KeyStore ks = KeyStore.getInstance("PKCS12");
        try {
            ks.load(null, null);
        } catch (IOException e) {
            throw new GeneralSecurityException(e);
        }
        int i = 0;
        for (X509Certificate c : certificates(caFile, "ca_file")) {
            ks.setCertificateEntry("ca" + i++, c);
        }
        TrustManagerFactory tmf = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm());
        tmf.init(ks);
        return tmf.getTrustManagers();
    }

    private static List<X509Certificate> certificates(String file, String field) throws GeneralSecurityException {
        byte[] pem = read(file, field);
        Collection<? extends Certificate> certs;
        try {
            certs = CertificateFactory.getInstance("X.509").generateCertificates(new ByteArrayInputStream(pem));
        } catch (GeneralSecurityException e) {
            throw new IllegalArgumentException("tls: " + field + " " + file + ": " + e.getMessage(), e);
        }
        if (certs.isEmpty()) {
            throw new IllegalArgumentException("tls: " + field + " " + file + ": no PEM certificates found");
        }
        List<X509Certificate> out = new ArrayList<>(certs.size());
        for (Certificate c : certs) {
            out.add((X509Certificate) c);
        }
        return out;
    }

    private PrivateKey privateKey() {
        String pem = new String(read(keyFile, "key_file"), StandardCharsets.US_ASCII);
        Matcher m = PEM_KEY.matcher(pem);
        if (!m.find()) {
            throw new IllegalArgumentException("tls: key_file " + keyFile + ": no PEM private key found");
        }
        if (!m.group(1).equals("PRIVATE KEY")) {
            throw new IllegalArgumentException("tls: key_file " + keyFile + ": " + m.group(1)
                    + " is not supported, convert it to unencrypted PKCS#8: "
                    + "openssl pkcs8 -topk8 -nocrypt -in <key> -out <key.pkcs8>");
        }
        byte[] der = Base64.getMimeDecoder().decode(m.group(2));
        for (String alg : List.of("RSA", "EC", "Ed25519")) {
            try {
                return KeyFactory.getInstance(alg).generatePrivate(new PKCS8EncodedKeySpec(der));
            } catch (GeneralSecurityException ignored) {
                // не тот алгоритм — пробуем следующий
            }
        }
        throw new IllegalArgumentException("tls: key_file " + keyFile + ": unsupported key algorithm");
    }

    /** Подписать ключом и проверить сертификатом — единый способ для RSA, EC и Ed25519. */
    private static boolean matches(PrivateKey key, X509Certificate cert) {
        String alg = switch (key.getAlgorithm()) {
            case "RSA" -> "SHA256withRSA";
            case "EC" -> "SHA256withECDSA";
            default -> key.getAlgorithm();
        };
        byte[] probe = "a2a-demo mTLS key check".getBytes(StandardCharsets.US_ASCII);
        try {
            Signature s = Signature.getInstance(alg);
            s.initSign(key);
            s.update(probe);
            byte[] sig = s.sign();
            Signature v = Signature.getInstance(alg);
            v.initVerify(cert.getPublicKey());
            v.update(probe);
            return v.verify(sig);
        } catch (GeneralSecurityException e) {
            return false;
        }
    }

    private static byte[] read(String file, String field) {
        try {
            return Files.readAllBytes(Path.of(file));
        } catch (IOException e) {
            throw new IllegalArgumentException("tls: " + field + ": " + e.getMessage(), e);
        }
    }

    /**
     * Принимает любой сертификат сервера. Именно X509ExtendedTrustManager:
     * для него JDK не добавляет своей проверки имени хоста поверх, и отключать
     * её глобальным системным свойством для всех агентов разом не приходится.
     */
    private static final class TrustAll extends X509ExtendedTrustManager {
        @Override
        public void checkClientTrusted(X509Certificate[] chain, String authType) {
        }

        @Override
        public void checkServerTrusted(X509Certificate[] chain, String authType) {
        }

        @Override
        public void checkClientTrusted(X509Certificate[] chain, String authType, Socket socket) {
        }

        @Override
        public void checkServerTrusted(X509Certificate[] chain, String authType, Socket socket) {
        }

        @Override
        public void checkClientTrusted(X509Certificate[] chain, String authType, SSLEngine engine) {
        }

        @Override
        public void checkServerTrusted(X509Certificate[] chain, String authType, SSLEngine engine) {
        }

        @Override
        public X509Certificate[] getAcceptedIssuers() {
            return new X509Certificate[0];
        }
    }
}
