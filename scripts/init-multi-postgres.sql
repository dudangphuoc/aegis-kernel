-- Khởi tạo database cho Control Plane
SELECT 'CREATE DATABASE cp_db' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'cp_db')\gexec
