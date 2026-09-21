-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725

-- Back to migration 39's sort_title, typo and all, and drop the two-argument
-- forms nothing older knows about.
DROP FUNCTION IF EXISTS natural_sort_key(TEXT, TEXT);
DROP FUNCTION IF EXISTS sort_title(TEXT, TEXT);

CREATE OR REPLACE FUNCTION sort_title(t TEXT)
RETURNS TEXT
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE
AS $$
  SELECT trim(
    regexp_replace(
      regexp_replace(
        trim(
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
