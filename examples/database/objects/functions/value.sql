CREATE OR ALTER FUNCTION dbo.saxbase_function()
RETURNS INT
AS
BEGIN
    DECLARE @count INT;
    SELECT @count = COUNT(*) FROM dbo.customers;
    RETURN @count;
END;
