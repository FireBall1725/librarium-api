-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725 (Adaléa)

-- One genre vocabulary, not two.
--
-- Books have always keyed genre through book_genres into the genres table,
-- which is a controlled list with a unique lowercased name. Series carried a
-- free-text TEXT[] that was never checked against anything: whatever the
-- provider said went in verbatim. The two were not two views of one thing, they
-- were two independent lists that happened to share eighteen words, and they
-- disagreed in the ways free text always does. "Science Fiction" and "Science
-- fiction" were two separate values on the same facet, "Sci-Fi" was a third,
-- and "Comics" sat beside them describing a format rather than a genre.
--
-- This is the join table that makes them one list. After it, a genre added for
-- a book is available to a series and the two facets cannot drift.

CREATE TABLE IF NOT EXISTS series_genres (
    series_id UUID NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    genre_id  UUID NOT NULL REFERENCES genres(id) ON DELETE CASCADE,
    PRIMARY KEY (series_id, genre_id)
);

CREATE INDEX IF NOT EXISTS series_genres_genre_id_idx ON series_genres(genre_id);

-- Genres the seeded list was missing.
--
-- All eight are real, and most are specific to manga and comics, which is where
-- the seed list was thinnest: it was written for a shelf of novels. They are
-- added rather than mapped away because there is nothing to map them to, and
-- dropping them would lose the only genre a run like this carries.
INSERT INTO genres (name)
SELECT name FROM (VALUES
    ('Superhero'), ('Tragedy'), ('Psychological'), ('Philosophical'),
    ('Classics'), ('LGBTQ'), ('Boys'' Love'), ('Girls'' Love')
) AS v(name)
WHERE NOT EXISTS (SELECT 1 FROM genres g WHERE g.name_lower = lower(v.name));

-- The mapping, applied once to what the providers have already written.
--
-- Three kinds of value are in that column and they need three different
-- answers. Spellings of a genre that already exists resolve to it. Values that
-- are not genres at all are dropped: Comics and Comics & Graphic Novels say
-- what media type already says, and Fiction and Literature say nothing that
-- narrows anything. Juvenile Fiction is the one non-genre with a real home,
-- since Children's was seeded from the start.
INSERT INTO series_genres (series_id, genre_id)
SELECT DISTINCT s.id, g.id
  FROM series s
  CROSS JOIN LATERAL unnest(s.genres) AS raw(value)
  JOIN genres g ON g.name_lower = lower(
        CASE lower(trim(raw.value))
            WHEN 'sci-fi'           THEN 'Science Fiction'
            WHEN 'sci fi'           THEN 'Science Fiction'
            WHEN 'juvenile fiction' THEN 'Children''s'
            ELSE trim(raw.value)
        END)
 WHERE lower(trim(raw.value)) NOT IN
       ('comics', 'comics & graphic novels', 'fiction', 'literature')
ON CONFLICT DO NOTHING;

-- series.genres is left in place and stops being read.
--
-- Dropping it in the same migration that fills its replacement would make this
-- irreversible against a database nobody has looked at yet, and the column is
-- the only record of what a provider actually said. It goes in a later release,
-- once the join table has been the thing answering for a while.
