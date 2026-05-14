-- +goose Up
-- Wiki-style memory pages (refs govega#71). Replaces the typed
-- user_memory + memory_items system in subsequent migrations.
-- path is a logical slash-separated address ("MEMORY.md",
-- "topics/sushi.md"), NOT a filesystem path.
CREATE TABLE IF NOT EXISTS memory_pages (
    scope       TEXT NOT NULL,
    scope_id    TEXT NOT NULL,
    user_id     TEXT NOT NULL,
    path        TEXT NOT NULL,
    content     TEXT NOT NULL DEFAULT '',
    frontmatter TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (scope, scope_id, user_id, path)
);
CREATE INDEX IF NOT EXISTS idx_memory_pages_updated
    ON memory_pages(scope, scope_id, user_id, updated_at DESC);

-- Directed link edges between memory pages. Extracted from page
-- content on every write so the graph endpoint is cheap.
CREATE TABLE IF NOT EXISTS memory_links (
    scope     TEXT NOT NULL,
    scope_id  TEXT NOT NULL,
    user_id   TEXT NOT NULL,
    from_path TEXT NOT NULL,
    to_path   TEXT NOT NULL,
    PRIMARY KEY (scope, scope_id, user_id, from_path, to_path)
);
CREATE INDEX IF NOT EXISTS idx_memory_links_to
    ON memory_links(scope, scope_id, user_id, to_path);

-- +goose Down
DROP TABLE IF EXISTS memory_links;
DROP TABLE IF EXISTS memory_pages;
