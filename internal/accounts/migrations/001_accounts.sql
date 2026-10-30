create table users (
    id            uuid primary key,
    email         text not null,
    name          text not null default '',
    password_hash text not null,
    role          text not null check (role in ('admin', 'user')),
    disabled_at   timestamptz,
    created_at    timestamptz not null default now(),
    last_login_at timestamptz
);

create unique index users_email_idx on users (lower(email));

create table sessions (
    id_hash      bytea primary key,
    user_id      uuid not null references users on delete cascade,
    created_at   timestamptz not null,
    last_seen_at timestamptz not null,
    expires_at   timestamptz not null,
    ip           text,
    user_agent   text
);

create index sessions_user_idx on sessions (user_id);

create table one_time_links (
    hash       bytea primary key,
    kind       text not null check (kind in ('setup', 'invite', 'reset')),
    user_id    uuid references users on delete cascade,
    email      text,
    role       text not null default 'user',
    created_by uuid references users on delete set null,
    created_at timestamptz not null default now(),
    expires_at timestamptz not null,
    used_at    timestamptz
);
