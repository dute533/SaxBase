-- migration Up
ALTER TABLE dbo.customers ADD nickname NVARCHAR(100) NULL;

-- migration Down
ALTER TABLE dbo.customers DROP COLUMN nickname;
