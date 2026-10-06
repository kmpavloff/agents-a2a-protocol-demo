Тестовые сертификаты для mTLS (EC P-256, срок 100 лет). Только для тестов —
секретов здесь нет. Ключи CA удалены: перевыпуск — заново всем набором.

- `ca.crt` — CA, подписавший `server.crt` (localhost, 127.0.0.1) и `client.crt`
- `other-ca.crt` — посторонний CA: сервер им не подписан
- `client.key` / `server.key` — PKCS#8; `client-sec1.key` — тот же ключ в SEC1
  (`BEGIN EC PRIVATE KEY`), который Java-порт должен отвергать с подсказкой
