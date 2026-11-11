-- +goose Up
ALTER TABLE dbo.customers ADD rollback_note NVARCHAR(100) NULL;

-- +goose Down
ALTER TABLE dbo.customers DROP COLUMN rollback_note;
