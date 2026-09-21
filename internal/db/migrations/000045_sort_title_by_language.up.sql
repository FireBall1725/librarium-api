-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725

-- sort_title has never stripped a leading article.
--
-- Its pattern was written as an E'' string, where \s is not an escape and
-- collapses to a plain "s". So the pattern was "an article, then one or more
-- letter s, no space": "The Restaurant at the End of the Universe" came back
-- unchanged and filed under T, while "Asimov's Mysteries" lost "As" (a, s) and
-- filed under "imov", and "Lost Boys" lost "Los" and filed under "t Boys".
-- 28 titles in one real catalogue were cut like that.
--
-- The list was also every article of twenty-odd languages applied to every
-- title. Once the space matches, that files an English "Die Trying" under
-- "Trying" and "Den of Thieves" under "of Thieves". So the strip now goes by
-- the language the book is in, and a title in a language with no list here is
-- left as it is. [[:space:]] instead of \s so the pattern means the same thing
-- whichever way standard_conforming_strings is set.
CREATE OR REPLACE FUNCTION sort_title(t TEXT, lang TEXT)
RETURNS TEXT
LANGUAGE sql IMMUTABLE PARALLEL SAFE
AS $$
  SELECT CASE
    WHEN x.pattern IS NULL THEN trim(x.folded)
    ELSE trim(regexp_replace(x.folded, x.pattern, '', 'i'))
  END
  FROM (
    SELECT
      -- Typographic punctuation folded to ASCII first, so L’Assommoir strips
      -- the same way L'Assommoir does (see migration 39).
      trim(translate(t,
        U&'\2018\2019\201C\201D\2013\2014\2212',
        '''''""---')) AS folded,
      -- No language recorded reads as English, which is what the catalogue is
      -- mostly in and what the old function meant to do for it.
      CASE lower(split_part(coalesce(nullif(trim(lang), ''), 'en'), '-', 1))
        WHEN 'en'  THEN '^(the|an|a)[[:space:]]+'
        WHEN 'eng' THEN '^(the|an|a)[[:space:]]+'
        WHEN 'fr'  THEN '^((les|le|la|une|un|des)[[:space:]]+|l'')'
        WHEN 'fre' THEN '^((les|le|la|une|un|des)[[:space:]]+|l'')'
        WHEN 'fra' THEN '^((les|le|la|une|un|des)[[:space:]]+|l'')'
        WHEN 'de'  THEN '^(der|die|das|den|dem|des|ein|eine|einen|einem|einer|eines)[[:space:]]+'
        WHEN 'ger' THEN '^(der|die|das|den|dem|des|ein|eine|einen|einem|einer|eines)[[:space:]]+'
        WHEN 'deu' THEN '^(der|die|das|den|dem|des|ein|eine|einen|einem|einer|eines)[[:space:]]+'
        WHEN 'es'  THEN '^(el|la|los|las|un|una|unos|unas)[[:space:]]+'
        WHEN 'spa' THEN '^(el|la|los|las|un|una|unos|unas)[[:space:]]+'
        WHEN 'it'  THEN '^((il|lo|la|i|gli|le|un|uno|una)[[:space:]]+|l'')'
        WHEN 'ita' THEN '^((il|lo|la|i|gli|le|un|uno|una)[[:space:]]+|l'')'
        WHEN 'pt'  THEN '^(o|a|os|as|um|uma|uns|umas)[[:space:]]+'
        WHEN 'por' THEN '^(o|a|os|as|um|uma|uns|umas)[[:space:]]+'
        WHEN 'nl'  THEN '^(de|het|een)[[:space:]]+'
        WHEN 'dut' THEN '^(de|het|een)[[:space:]]+'
        WHEN 'nld' THEN '^(de|het|een)[[:space:]]+'
      END AS pattern
  ) x
$$;

-- The one-argument form keeps its callers (the letter filter, the letter index,
-- the grouped list) and reads as English.
CREATE OR REPLACE FUNCTION sort_title(t TEXT)
RETURNS TEXT
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE
AS $$
  SELECT sort_title(t, 'en');
$$;

-- natural_sort_key with the language passed through, for the book list.
CREATE OR REPLACE FUNCTION natural_sort_key(t TEXT, lang TEXT)
RETURNS TEXT
LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE
AS $$
DECLARE
  result    TEXT := '';
  remaining TEXT := lower(sort_title(t, lang));
  m         TEXT[];
BEGIN
  IF t IS NULL THEN
    RETURN NULL;
  END IF;
  LOOP
    m := regexp_match(remaining, '^(.*?)([0-9]+)(.*)$');
    EXIT WHEN m IS NULL;
    result    := result || m[1] || lpad(m[2], 10, '0');
    remaining := m[3];
  END LOOP;
  RETURN result || remaining;
END;
$$;
