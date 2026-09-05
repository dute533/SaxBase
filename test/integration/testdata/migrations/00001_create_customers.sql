-- +goose Up
CREATE TABLE dbo.customers (
    id INT NOT NULL PRIMARY KEY,
    name NVARCHAR(100) NOT NULL
);
INSERT INTO dbo.customers (id, name) VALUES (1, N'Ada');

-- +goose Down
DROP TABLE dbo.customers;
