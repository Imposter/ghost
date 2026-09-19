-- Per-network switch for interactive enrolment codes. Existing networks keep
-- accepting them (1); pre-auth keys are unaffected either way.

ALTER TABLE networks ADD COLUMN interactive_enrollment BIGINT NOT NULL DEFAULT 1;
