-- Conditional-GET validators for feed polling. Opaque header values from the
-- feed server (ETag / Last-Modified), sent back verbatim on the next poll;
-- empty means no validator known.
ALTER TABLE feeds ADD COLUMN etag text NOT NULL DEFAULT '';
ALTER TABLE feeds ADD COLUMN last_modified text NOT NULL DEFAULT '';
