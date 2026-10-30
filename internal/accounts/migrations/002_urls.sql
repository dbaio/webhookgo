create table urls (
    token_id   uuid primary key,
    owner_id   uuid not null references users on delete cascade,
    config     jsonb not null,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

create index urls_owner_idx on urls (owner_id, created_at desc);
