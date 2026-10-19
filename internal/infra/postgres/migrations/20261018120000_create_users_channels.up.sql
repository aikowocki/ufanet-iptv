CREATE TABLE users (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    login         TEXT        NOT NULL UNIQUE CHECK (login <> ''),
    password_hash TEXT        NOT NULL,
    is_active     BOOLEAN     NOT NULL DEFAULT TRUE,
    is_admin      BOOLEAN     NOT NULL DEFAULT FALSE,
    -- TRUE: после входа нужно потребовать смену пароля (начальный пароль известен).
    must_change_password BOOLEAN NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE channels (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    number      INTEGER NOT NULL UNIQUE CHECK (number > 0),
    name        TEXT    NOT NULL CHECK (name <> ''),
    -- Путь к HLS-плейлисту относительно STREAM_BASE_URL.
    stream_path TEXT    NOT NULL CHECK (stream_path <> ''),
    is_active   BOOLEAN NOT NULL DEFAULT TRUE
);
