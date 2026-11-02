-- Reading tracker schema (SQLite)
--
-- Run with:  sqlite3 reading.db ".read schema.sql"

-- SQLite does NOT enforce foreign keys unless you turn them on, and the
-- setting only lasts for the current connection. Every new connection
-- (including each one in your Go program's connection pool) must turn it on again.
PRAGMA foreign_keys = ON;


CREATE TABLE IF NOT EXISTS books (
    -- In SQLite, "INTEGER PRIMARY KEY" already assigns IDs automatically.
    -- AUTOINCREMENT only adds a guarantee that IDs of deleted rows are never
    -- reused. Since there's no delete feature, we don't need it.
    book_id     INTEGER PRIMARY KEY,

    title       TEXT    NOT NULL,
    author      TEXT    NOT NULL,

    -- Your "version number" for books with the same title and author.
    -- Defaults to 1, so you only set it when it matters.
    edition     INTEGER NOT NULL DEFAULT 1 CHECK (edition >= 1),

    -- Needed for the progress pie chart. Must be a positive number.
    total_pages INTEGER NOT NULL CHECK (total_pages > 0),

    -- Replaces the "completed" boolean. One column with a fixed set of values
    -- means a book can never be "completed" and "abandoned" at the same time.
    -- New books start as 'reading'. Abandoning a book is an update, not a delete.
    status      TEXT    NOT NULL DEFAULT 'reading'
                CHECK (status IN ('reading', 'completed', 'abandoned')),

    -- SQLite has no date type. We store dates as ISO 8601 text in UTC,
    -- e.g. '2026-10-30T14:05:00Z'. This format sorts correctly as text,
    -- is readable in sqlite3, and Go's time package can parse it.
    created_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),

    -- A book is identified by the COMBINATION of these three columns.
    -- Two books can share a title, as long as author or edition differ.
    UNIQUE (title, author, edition)
);


CREATE TABLE IF NOT EXISTS reading_sessions (
    session_id INTEGER PRIMARY KEY,

    -- NOT NULL: a session must always belong to a book.
    -- REFERENCES: this is the foreign key to books.
    -- ON DELETE RESTRICT: if anyone deletes a book by hand while it has
    -- sessions, the database refuses. Your history can't silently disappear.
    book_id    INTEGER NOT NULL
               REFERENCES books(book_id) ON DELETE RESTRICT,

    -- Think of these as bookmark positions: where the bookmark was before
    -- and after the session. Pages read = end_page - start_page.
    -- A brand new book starts at page 0.
    start_page INTEGER NOT NULL CHECK (start_page >= 0),
    end_page   INTEGER NOT NULL,

    -- When the session happened (date and time, UTC). Defaults to now,
    -- but you can set it yourself when logging a session afterwards.
    read_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),

    -- A table-level check: you must have read at least one page.
    CHECK (end_page > start_page)
);

-- You'll often look up "all sessions for book X". This index makes that
-- fast. SQLite does not create indexes on foreign key columns automatically.
CREATE INDEX IF NOT EXISTS idx_reading_sessions_book_id
    ON reading_sessions (book_id);
