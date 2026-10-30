create table abuse_reports (
    id          uuid primary key,
    token_id    uuid not null,
    reporter_ip text not null,
    contact     text not null default '',
    message     text not null default '',
    status      text not null default 'open' check (status in ('open', 'actioned', 'dismissed')),
    handled_by  uuid references users on delete set null,
    handled_at  timestamptz,
    note        text not null default '',
    created_at  timestamptz not null default now()
);

create index abuse_reports_status_idx on abuse_reports (status, created_at desc);
create index abuse_reports_token_idx on abuse_reports (token_id);
