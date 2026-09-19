-- Initial schema: users + raw_messages.
-- This is the source of truth for the deployed database (applied by
-- `supabase db push`). It mirrors the GORM models in model/.

create table if not exists users (
    id            uuid primary key,
    email         text not null unique,
    password_hash text not null,
    name          text,
    created_at    timestamptz,
    updated_at    timestamptz
);

create table if not exists raw_messages (
    id           uuid primary key,
    user_id      text not null,
    sender       text,
    body         text not null,
    source       text not null,
    received_at  timestamptz not null,
    status       text not null,
    content_hash text not null unique,
    created_at   timestamptz,
    updated_at   timestamptz
);

create index if not exists idx_raw_messages_user_id     on raw_messages (user_id);
create index if not exists idx_raw_messages_sender      on raw_messages (sender);
create index if not exists idx_raw_messages_received_at on raw_messages (received_at);
create index if not exists idx_raw_messages_status      on raw_messages (status);
