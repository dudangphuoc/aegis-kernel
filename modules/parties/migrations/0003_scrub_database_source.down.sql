-- Không khôi phục được DSN đã xóa (cố ý)
ALTER TABLE parties ALTER COLUMN database_source DROP DEFAULT;
