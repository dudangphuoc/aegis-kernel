.PHONY: build-modules build-kernel build-platform proto run run-ui test test-pgbouncer clean docker-up docker-down

# 1. Khởi động hạ tầng PostgreSQL Cluster
docker-up:
	docker compose up -d postgres pgbouncer-node-1 pgbouncer-node-2

docker-down:
	docker compose down

# 2. Biên dịch các module microservices ra thư mục bin/modules
build-modules:
	@mkdir -p bin/modules
	go build -o bin/modules/auth ./modules/auth/cmd
	go build -o bin/modules/parties ./modules/parties/cmd
	go build -o bin/modules/audit ./modules/audit/cmd

# 3. Biên dịch Core Kernel Launcher
build-kernel:
	@mkdir -p bin
	go build -o bin/kernel ./launcher

# CLI nền tảng (platform db new|lint|plan|apply|explain)
build-platform:
	@mkdir -p bin
	go build -o bin/platform ./cmd/platform

# Sinh mã Go từ proto (cần buf, protoc-gen-go, protoc-gen-go-grpc trong PATH)
proto:
	cd framework/core/wire && buf lint && buf generate

# 4. Chạy toàn bộ hệ thống Micro-Kernel Backend (Port 8080)
run: build-modules
	go run ./launcher

# 5. Chạy React Control Plane Admin Dashboard (Port 5173)
run-ui:
	cd web && npm run dev

# 6. Chạy Unit Test + Race Detector trên toàn bộ codebase
test:
	go test -v -race ./...

# 6b. Chạy integration test của module qua PgBouncer (transaction pooling + binary_parameters)
test-pgbouncer:
	TEST_PGBOUNCER_ADDR=localhost:6432 go test -race -count=1 ./modules/...

# 7. Dọn dẹp
clean:
	rm -rf bin/
