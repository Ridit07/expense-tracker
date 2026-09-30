-- Adds raw_messages.external_id: the source system's own stable id for a
-- message (chat.db's message.guid for the macOS ingestion path).
--
-- The two dedup keys are kept in disjoint domains via partial unique indexes:
-- external_id governs rows that have one, content_hash governs the rest. That
-- way an INSERT ... ON CONFLICT naming one target can never trip the other,
-- which would abort a whole backfill batch on an unrelated collision.
--
-- Existing rows are backfilled with '' rather than NULL so both predicates are
-- always decidable (NULL <> '' is NULL, which would silently drop those rows
-- out of the index).

alter table raw_messages
    add column if not exists external_id text not null default '';

-- content_hash was globally unique; it is now unique only among rows that have
-- no external_id. Drop whichever form the environment has: `unique` in the
-- column definition creates a constraint, GORM's AutoMigrate creates an index.
alter table raw_messages drop constraint if exists raw_messages_content_hash_key;
drop index if exists idx_raw_messages_content_hash;

create unique index if not exists idx_raw_messages_content_hash
    on raw_messages (content_hash)
    where external_id = '';

create unique index if not exists idx_raw_messages_user_external_id
    on raw_messages (user_id, external_id)
    where external_id <> '';
