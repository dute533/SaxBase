CREATE OR ALTER PROCEDURE dbo.saxbase_get_value AS
BEGIN
    SET NOCOUNT ON;
    SELECT id AS value FROM dbo.customers ORDER BY id;
END;
