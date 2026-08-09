-- Whether a live card belongs in this chat, decided when the card is opened.
--
-- A one-to-one chat is a private waiting room: the asker is the only reader and
-- watching the answer form is the point. A group's main feed is shared, and a
-- card repainting every second and a half spends everyone's attention to serve
-- one person — there the inbound Typing reaction says work is under way and the
-- answer arrives as a single message. A group topic is isolated enough to read
-- like a private chat, so it keeps the card.
--
-- The row is still created for a suppressed card, because it is also the ledger
-- that lets exactly one terminal event answer a task.
--
-- Snapshotted rather than derived at paint time: a binding's last_thread_id
-- tracks the most recent inbound trigger and can change while a task runs,
-- which would otherwise flip the decision mid-answer.
ALTER TABLE channel_outbound_card_message
    ADD COLUMN card_suppressed BOOLEAN NOT NULL DEFAULT false;
