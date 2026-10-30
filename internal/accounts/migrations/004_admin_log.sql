create table admin_log (
    id          uuid primary key,
    actor_id    uuid references users on delete set null,
    actor_email text not null,
    action      text not null,
    target_type text not null check (target_type in ('user', 'url', 'link')),
    target_id   text not null,
    details     jsonb not null default '{}',
    ip          text,
    created_at  timestamptz not null default now()
);

create index admin_log_created_idx on admin_log (created_at desc);
