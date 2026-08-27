-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725 (Adaléa)

-- A curly apostrophe sorted somewhere a straight one did not.
--
-- Three volumes of one run came back 1, 3, 2, because volume two was catalogued
-- as "Can’t Fear Your Own World" with U+2019 while its siblings used U+0027.
-- The two characters are the same apostrophe to a reader and forty-eight code
-- points apart to a sort, so every straight-quoted title in a run groups ahead
-- of every curly-quoted one and the numbering falls apart in between.
--
-- Providers are where this comes from and they are not consistent with
-- themselves: the same series arrives with both, depending on which record was
-- typed by whom. Normalising the stored title would be the other fix and is
-- wrong, because the title is what the publisher printed and a catalogue should
-- keep it. So the folding belongs in the sort key, which exists precisely to be
-- the reading of a title rather than the title.
--
-- Dashes and double quotes go the same way for the same reason. They are less
-- common in titles and split a sort identically when they turn up.
CREATE OR REPLACE FUNCTION sort_title(t TEXT)
RETURNS TEXT
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE
AS $$
  SELECT trim(
    regexp_replace(
      regexp_replace(
        trim(
          -- Typographic punctuation folded to ASCII before anything else, so
          -- the article strip below sees L’Assommoir the same way it already
          -- sees L'Assommoir.
          translate(t,
            U&'\2018\2019\201C\201D\2013\2014\2212',
            '''''""---')
        ),
        E'^(unos|unas|eine|the|les|una|une|des|los|las|gli|het|een|das|der|die|dem|den|det|ein|ett|uma|um|os|as|le|la|lo|el|un|de|na|az|yr|il|et|en|y|an|o|a)\s+',
        '',
        'i'
      ),
      E'^l''',
      '',
      'i'
    )
  );
$$;
