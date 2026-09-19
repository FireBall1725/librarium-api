-- Which ISBN prefix goes with which UPC company, learned from real books.
--
-- A mass-market paperback's UPC is shared by every book at the same price;
-- the 5-digit add-on beside it is the middle of the ISBN-10. Given the
-- publisher's ISBN prefix, the add-on names the book. The code carries the
-- pairings checked by hand; this table holds the ones an instance learned
-- when someone scanned a back cover and then the book's ISBN, and the add-on
-- matched the ISBN's digits.
CREATE TABLE IF NOT EXISTS upc_isbn_prefixes (
    upc_prefix        TEXT        NOT NULL CHECK (upc_prefix ~ '^[0-9]{6}$'),
    isbn_prefix       TEXT        NOT NULL CHECK (isbn_prefix ~ '^[0-9]{4}$'),
    learned_from_isbn TEXT        NOT NULL,
    learned_by        UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (upc_prefix, isbn_prefix)
);
