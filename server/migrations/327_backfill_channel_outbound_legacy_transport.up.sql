-- Migration 325 gave every existing row transport='cardkit', but a row written
-- before that migration was produced by the message-patch design: it carries a
-- delivered message and no CardKit entity. Left as 'cardkit' those rows route
-- into the entity APIs with an empty card id, which fails before the request
-- is even sent and never recovers.
--
-- A row that already has a message but no entity can only have come from the
-- old design, so it is labelled for what it is.
UPDATE channel_outbound_card_message
SET transport = 'legacy'
WHERE transport = 'cardkit'
  AND channel_card_id = ''
  AND channel_card_message_id <> '';
