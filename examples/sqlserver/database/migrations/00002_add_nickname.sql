-- +goose Up
ALTER TABLE dbo.customers ADD nickname NVARCHAR(100) NULL;

-- +goose Down
ALTER TABLE dbo.customers DROP COLUMN nickname;
