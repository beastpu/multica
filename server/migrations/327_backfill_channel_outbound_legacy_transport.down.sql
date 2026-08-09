-- No down: the pre-migration transport of these rows is not recoverable, and
-- 'legacy' is the correct label for a card with no CardKit entity either way.
SELECT 1;
