-- Администратор нужен в любом окружении, иначе некому управлять сервисом.
-- Начальный пароль admin123 ОБЯЗАТЕЛЬНО сменить после первого входа:
-- must_change_password = TRUE сообщает об этом приложению.
INSERT INTO users (login, password_hash, is_admin, must_change_password)
VALUES ('admin', '$argon2id$v=19$m=19456,t=2,p=1$ekYMdEw2Sf5agswILDTp4w$hwAIdqX99F9JrwI2oKeg7yCiioyUpYgQbZBvmmv+Zi4', TRUE, TRUE)
ON CONFLICT (login) DO NOTHING;
