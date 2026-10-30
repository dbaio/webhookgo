create table api_keys (
    id           uuid primary key,
    user_id      uuid not null references users on delete cascade,
    name         text not null,
    prefix       text not null,
    hash         bytea not null unique,
    created_at   timestamptz not null default now(),
    last_used_at timestamptz,
    revoked_at   timestamptz
);

create index api_keys_user_idx on api_keys (user_id);
