-- migration Up
ALTER TABLE dbo.customers ADD rollback_note NVARCHAR(100) NULL;

-- migration Down
ALTER TABLE dbo.customers DROP COLUMN rollback_note;
