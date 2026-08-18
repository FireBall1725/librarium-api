-- Remove the "Favourites" shelf that used to be created with every library.
--
-- Every new library was given one, which made the sidebar list the same name
-- once per library: a shelf belongs to one library, so two called Favourites
-- are two different shelves and nothing said which was which. Favouriting is a
-- per-book flag, so it is a rule, and a rule belongs in a view — which is what
-- it is now. The shelf was a second way to say the same thing.
--
-- Only the untouched ones go. The service created them with exactly this shape
-- (name, no description, no colour, the star emoji, order 0), so anything that
-- differs is a shelf somebody edited, and anything holding books or tags is a
-- shelf somebody used. Those are left alone: they are that person's curation,
-- and a migration is not the place to overrule it. They will simply coexist
-- with the new view, labelled with their library in the rail.
--
-- Deliberately not converting a populated shelf's books into is_favorite. A
-- shelf belongs to a library and the flag belongs to a user, so there is no
-- honest answer to whose favourites those books become in a library with more
-- than one member.
DELETE FROM shelves s
WHERE s.name = 'Favourites'
  AND COALESCE(s.description, '') = ''
  AND COALESCE(s.color, '') = ''
  AND COALESCE(s.icon, '') = '⭐'
  AND COALESCE(s.display_order, 0) = 0
  AND NOT EXISTS (SELECT 1 FROM book_shelves bs WHERE bs.shelf_id = s.id)
  AND NOT EXISTS (SELECT 1 FROM shelf_tags st WHERE st.shelf_id = s.id);
