-- database_source từng lưu DSN đầy đủ (kèm mật khẩu Postgres) trên mỗi dòng: xóa sạch và ngừng sử dụng.
-- Giữ cột (rỗng) để code cũ/mới cùng chạy được trong lúc migrate; có thể DROP ở migration sau.
UPDATE parties SET database_source = '' WHERE database_source <> '';
ALTER TABLE parties ALTER COLUMN database_source SET DEFAULT '';
