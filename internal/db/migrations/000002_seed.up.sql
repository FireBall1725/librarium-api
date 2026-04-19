-- Seed required reference data
-- SPDX-License-Identifier: AGPL-3.0-only

-- ============================================================
-- Media Types
-- ============================================================

INSERT INTO media_types (id, name, display_name, description) VALUES
    -- Books & print
    (gen_random_uuid(), 'novel',                   'Novel',                   'Full-length fiction or non-fiction book'),
    (gen_random_uuid(), 'manga',                   'Manga',                   'Japanese comic book / graphic novel'),
    (gen_random_uuid(), 'manhwa',                  'Manhwa',                  'Korean comic / webtoon'),
    (gen_random_uuid(), 'manhua',                  'Manhua',                  'Chinese comic'),
    (gen_random_uuid(), 'light_novel',             'Light Novel',             'Japanese illustrated prose novel (e.g. SAO, Re:Zero)'),
    (gen_random_uuid(), 'comic',                   'Comic Book',              'American/Western comic book issue'),
    (gen_random_uuid(), 'graphic_novel',           'Graphic Novel',           'Long-form comic narrative in book format'),
    (gen_random_uuid(), 'artbook',                 'Art Book',                'Illustration or art collection'),
    (gen_random_uuid(), 'magazine',                'Magazine',                'Periodical publication'),
    (gen_random_uuid(), 'short_story_collection',  'Short Story Collection',  'Anthology of short fiction by one author'),
    (gen_random_uuid(), 'anthology',               'Anthology',               'Collection of works by multiple authors'),
    (gen_random_uuid(), 'non_fiction',             'Non-Fiction',             'Factual prose — biography, history, science, etc.'),
    (gen_random_uuid(), 'textbook',                'Textbook',                'Educational or academic text'),
    (gen_random_uuid(), 'rpg_sourcebook',          'RPG Sourcebook',          'Tabletop role-playing game supplement or core rulebook'),
    -- Documents & technical
    (gen_random_uuid(), 'datasheet',               'Datasheet',               'Component or product specification sheet'),
    (gen_random_uuid(), 'technical_manual',        'Technical Manual',        'In-depth technical reference or service manual'),
    (gen_random_uuid(), 'white_paper',             'White Paper',             'Authoritative report or guide on a topic'),
    (gen_random_uuid(), 'appliance_manual',        'Appliance Manual',        'Owner or installation manual for an appliance'),
    (gen_random_uuid(), 'user_guide',              'User Guide',              'General usage instructions for a product'),
    (gen_random_uuid(), 'specification',           'Specification',           'Formal technical specification document');

-- ============================================================
-- Canonical Genres
-- ============================================================

INSERT INTO genres (name) VALUES
    ('Action'),
    ('Adventure'),
    ('Biography'),
    ('Children''s'),
    ('Comedy'),
    ('Crime'),
    ('Drama'),
    ('Fantasy'),
    ('Graphic Novel'),
    ('Historical'),
    ('Horror'),
    ('Literary Fiction'),
    ('Manga'),
    ('Mystery'),
    ('Poetry'),
    ('Romance'),
    ('Science Fiction'),
    ('Self-Help'),
    ('Slice of Life'),
    ('Sports'),
    ('Supernatural'),
    ('Thriller'),
    ('Travel'),
    ('Western'),
    ('Young Adult')
ON CONFLICT (name_lower) DO NOTHING;

-- ============================================================
-- Built-in Roles
-- ============================================================

INSERT INTO roles (id, name, description, is_system) VALUES
    (gen_random_uuid(), 'instance_admin',  'Full control over the Librarium instance and all libraries', TRUE),
    (gen_random_uuid(), 'library_owner',   'Full control over a specific library and its members',       TRUE),
    (gen_random_uuid(), 'library_editor',  'Can add and edit books, shelves, tags, and loans',           TRUE),
    (gen_random_uuid(), 'library_viewer',  'Read-only access to a library',                              TRUE);

-- ============================================================
-- Permissions
-- ============================================================

INSERT INTO permissions (id, name, description, resource, action) VALUES
    -- Books
    (gen_random_uuid(), 'books:create',   'Add books to a library',          'books', 'create'),
    (gen_random_uuid(), 'books:read',     'View books in a library',          'books', 'read'),
    (gen_random_uuid(), 'books:update',   'Edit book metadata',               'books', 'update'),
    (gen_random_uuid(), 'books:delete',   'Remove books from a library',      'books', 'delete'),
    -- Editions
    (gen_random_uuid(), 'editions:create', 'Add editions to a book',          'editions', 'create'),
    (gen_random_uuid(), 'editions:read',   'View editions of a book',         'editions', 'read'),
    (gen_random_uuid(), 'editions:update', 'Edit edition details',            'editions', 'update'),
    (gen_random_uuid(), 'editions:delete', 'Remove an edition',               'editions', 'delete'),
    -- Covers
    (gen_random_uuid(), 'covers:create',  'Upload cover images',              'covers', 'create'),
    (gen_random_uuid(), 'covers:read',    'View cover images',                'covers', 'read'),
    (gen_random_uuid(), 'covers:delete',  'Delete cover images',              'covers', 'delete'),
    -- Series
    (gen_random_uuid(), 'series:create',  'Create series records',            'series', 'create'),
    (gen_random_uuid(), 'series:read',    'View series and entries',          'series', 'read'),
    (gen_random_uuid(), 'series:update',  'Edit series records',              'series', 'update'),
    (gen_random_uuid(), 'series:delete',  'Delete series records',            'series', 'delete'),
    -- Contributors
    (gen_random_uuid(), 'contributors:create', 'Create contributor records',  'contributors', 'create'),
    (gen_random_uuid(), 'contributors:read',   'View contributors',           'contributors', 'read'),
    (gen_random_uuid(), 'contributors:update', 'Edit contributor records',    'contributors', 'update'),
    (gen_random_uuid(), 'contributors:delete', 'Delete contributor records',  'contributors', 'delete'),
    -- Shelves
    (gen_random_uuid(), 'shelves:create', 'Create shelves',                   'shelves', 'create'),
    (gen_random_uuid(), 'shelves:read',   'View shelves and their books',     'shelves', 'read'),
    (gen_random_uuid(), 'shelves:update', 'Edit shelves',                     'shelves', 'update'),
    (gen_random_uuid(), 'shelves:delete', 'Delete shelves',                   'shelves', 'delete'),
    -- Tags
    (gen_random_uuid(), 'tags:create',    'Create tags',                      'tags', 'create'),
    (gen_random_uuid(), 'tags:read',      'View tags',                        'tags', 'read'),
    (gen_random_uuid(), 'tags:update',    'Edit tags',                        'tags', 'update'),
    (gen_random_uuid(), 'tags:delete',    'Delete tags',                      'tags', 'delete'),
    -- Loans
    (gen_random_uuid(), 'loans:create',   'Record a loan',                    'loans', 'create'),
    (gen_random_uuid(), 'loans:read',     'View loans',                       'loans', 'read'),
    (gen_random_uuid(), 'loans:update',   'Update loan details or return',    'loans', 'update'),
    (gen_random_uuid(), 'loans:delete',   'Delete loan records',              'loans', 'delete'),
    -- Wishlist
    (gen_random_uuid(), 'wishlist:create', 'Add wishlist items',              'wishlist', 'create'),
    (gen_random_uuid(), 'wishlist:read',   'View wishlist',                   'wishlist', 'read'),
    (gen_random_uuid(), 'wishlist:update', 'Edit wishlist items',             'wishlist', 'update'),
    (gen_random_uuid(), 'wishlist:delete', 'Remove wishlist items',           'wishlist', 'delete'),
    -- Library management
    (gen_random_uuid(), 'library:read',   'View library details',             'library', 'read'),
    (gen_random_uuid(), 'library:update', 'Edit library settings',            'library', 'update'),
    (gen_random_uuid(), 'library:delete', 'Delete a library',                 'library', 'delete'),
    (gen_random_uuid(), 'library:admin',  'Full library administration',      'library', 'admin'),
    -- Members
    (gen_random_uuid(), 'members:create', 'Invite members to a library',      'members', 'create'),
    (gen_random_uuid(), 'members:read',   'View library members',             'members', 'read'),
    (gen_random_uuid(), 'members:update', 'Change member roles',              'members', 'update'),
    (gen_random_uuid(), 'members:delete', 'Remove members from a library',    'members', 'delete'),
    -- Storage locations
    (gen_random_uuid(), 'storage:create', 'Add storage locations',            'storage', 'create'),
    (gen_random_uuid(), 'storage:read',   'View storage locations',           'storage', 'read'),
    (gen_random_uuid(), 'storage:update', 'Edit storage locations',           'storage', 'update'),
    (gen_random_uuid(), 'storage:delete', 'Remove storage locations',         'storage', 'delete'),
    -- Import / Export
    (gen_random_uuid(), 'import:create',  'Start an import job',              'import', 'create'),
    (gen_random_uuid(), 'import:read',    'View import job status',           'import', 'read'),
    (gen_random_uuid(), 'export:read',    'Export library data',              'export', 'read'),
    -- Instance admin
    (gen_random_uuid(), 'admin:users:create',   'Create users',               'admin:users', 'create'),
    (gen_random_uuid(), 'admin:users:read',     'View all users',             'admin:users', 'read'),
    (gen_random_uuid(), 'admin:users:update',   'Edit users',                 'admin:users', 'update'),
    (gen_random_uuid(), 'admin:users:delete',   'Delete users',               'admin:users', 'delete'),
    (gen_random_uuid(), 'admin:roles:create',   'Create custom roles',        'admin:roles', 'create'),
    (gen_random_uuid(), 'admin:roles:read',     'View roles',                 'admin:roles', 'read'),
    (gen_random_uuid(), 'admin:roles:update',   'Edit custom roles',          'admin:roles', 'update'),
    (gen_random_uuid(), 'admin:roles:delete',   'Delete custom roles',        'admin:roles', 'delete'),
    (gen_random_uuid(), 'admin:settings:read',  'View instance settings',     'admin:settings', 'read'),
    (gen_random_uuid(), 'admin:settings:update','Update instance settings',   'admin:settings', 'update');

-- ============================================================
-- Role → Permission assignments
-- ============================================================

-- instance_admin gets every permission
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r, permissions p
WHERE r.name = 'instance_admin';

-- library_owner gets all library-scoped permissions (not instance admin)
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.name IN (
    'books:create',   'books:read',   'books:update',   'books:delete',
    'editions:create','editions:read','editions:update','editions:delete',
    'covers:create',  'covers:read',  'covers:delete',
    'series:create',  'series:read',  'series:update',  'series:delete',
    'contributors:create','contributors:read','contributors:update','contributors:delete',
    'shelves:create', 'shelves:read', 'shelves:update', 'shelves:delete',
    'tags:create',    'tags:read',    'tags:update',    'tags:delete',
    'loans:create',   'loans:read',   'loans:update',   'loans:delete',
    'wishlist:create','wishlist:read','wishlist:update','wishlist:delete',
    'library:read',   'library:update','library:delete','library:admin',
    'members:create', 'members:read', 'members:update', 'members:delete',
    'storage:create', 'storage:read', 'storage:update', 'storage:delete',
    'import:create',  'import:read',  'export:read'
)
WHERE r.name = 'library_owner';

-- library_editor: can manage content but not library settings or members
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.name IN (
    'books:create',   'books:read',   'books:update',   'books:delete',
    'editions:create','editions:read','editions:update','editions:delete',
    'covers:create',  'covers:read',  'covers:delete',
    'series:create',  'series:read',  'series:update',
    'contributors:create','contributors:read','contributors:update',
    'shelves:create', 'shelves:read', 'shelves:update', 'shelves:delete',
    'tags:create',    'tags:read',    'tags:update',    'tags:delete',
    'loans:create',   'loans:read',   'loans:update',
    'wishlist:create','wishlist:read','wishlist:update','wishlist:delete',
    'library:read',
    'members:read',
    'storage:read',
    'import:create',  'import:read',  'export:read'
)
WHERE r.name = 'library_editor';

-- library_viewer: read-only
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.name IN (
    'books:read',
    'editions:read',
    'covers:read',
    'series:read',
    'contributors:read',
    'shelves:read',
    'tags:read',
    'loans:read',
    'wishlist:read',
    'library:read',
    'members:read',
    'export:read'
)
WHERE r.name = 'library_viewer';
