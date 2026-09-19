-- How each peer enrolled: 'auth_key', 'interactive' or 'direct'. Existing
-- peers are backfilled where the method can be recovered: a pre-auth key id
-- means auth_key, and a claimed enrolment that still names the peer means
-- interactive. The rest (direct creations, and interactive enrolments already
-- pruned) stay '' (unknown).

ALTER TABLE peers ADD COLUMN enrollment_method TEXT NOT NULL DEFAULT '';
UPDATE peers SET enrollment_method = 'auth_key' WHERE auth_key_id <> '';
UPDATE peers SET enrollment_method = 'interactive' WHERE enrollment_method = '' AND id IN (SELECT peer_id FROM enrollments WHERE peer_id <> '');
