-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2026 FireBall1725 (Adaléa)

-- Back to the definition from the initial schema, which sorts a curly
-- apostrophe apart from a straight one.
CREATE OR REPLACE FUNCTION sort_title(t TEXT)
RETURNS TEXT
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE
AS $$
  SELECT trim(
    regexp_replace(
      regexp_replace(
        trim(t),
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
