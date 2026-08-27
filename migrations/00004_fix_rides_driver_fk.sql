-- +goose Up
-- Fix rides.driver_id FK to allow driver deletion when rides exist.
-- Previous: NO ACTION (a) blocked DELETE FROM drivers when rides reference it.
-- New: SET NULL preserves ride history with driver_id = NULL.

ALTER TABLE rides DROP CONSTRAINT IF EXISTS rides_driver_id_fkey;
ALTER TABLE rides ADD CONSTRAINT rides_driver_id_fkey
  FOREIGN KEY (driver_id) REFERENCES drivers(id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE rides DROP CONSTRAINT IF EXISTS rides_driver_id_fkey;
ALTER TABLE rides ADD CONSTRAINT rides_driver_id_fkey
  FOREIGN KEY (driver_id) REFERENCES drivers(id);
