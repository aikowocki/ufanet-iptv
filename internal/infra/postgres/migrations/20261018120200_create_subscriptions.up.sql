CREATE TABLE packages (
    id        BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name      TEXT    NOT NULL UNIQUE CHECK (name <> ''),
    is_active BOOLEAN NOT NULL DEFAULT TRUE
);

CREATE TABLE package_channels (
    package_id BIGINT NOT NULL REFERENCES packages (id) ON DELETE CASCADE,
    channel_id BIGINT NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    PRIMARY KEY (package_id, channel_id)
);

-- Обратный поиск «в какие пакеты входит канал».
CREATE INDEX package_channels_channel_id_idx ON package_channels (channel_id);

CREATE TABLE subscriptions (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    package_id BIGINT      NOT NULL REFERENCES packages (id) ON DELETE CASCADE,
    -- Ручное включение и выключение администратором.
    is_active  BOOLEAN     NOT NULL DEFAULT TRUE,
    -- NULL означает бессрочную подписку; иначе доступ пропадает сам в этот момент.
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, package_id)
);
