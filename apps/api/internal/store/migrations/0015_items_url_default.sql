-- Quote items (created from highlights) have no url of their own; give the
-- column a default so CreateQuoteItem's insert (which omits url) succeeds.
ALTER TABLE items ALTER COLUMN url SET DEFAULT '';
