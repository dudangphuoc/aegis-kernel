.PHONY: build-modules build-kernel run run-ui test clean docker-up docker-down

# 1. Khởi động hạ tầng PostgreSQL Cluster
docker-up:
	docker compose up -d postgres

docker-down:
	docker compose down

# 2. Biên dịch các module microservices ra thư mục bin/modules
build-modules:
	@mkdir -p bin/modules
	go build -o bin/modules/auth ./modules/auth/cmd
	go build -o bin/modules/parties ./modules/parties/cmd

# 3. Biên dịch Core Kernel Launcher
build-kernel:
	@mkdir -p bin
	go build -o bin/kernel ./launcher

# 4. Chạy toàn bộ hệ thống Micro-Kernel Backend (Port 8080)
run: build-modules
	go run ./launcher

# 5. Chạy React Control Plane Admin Dashboard (Port 5173)
run-ui:
	cd web && npm run dev

# 6. Chạy Unit Test + Race Detector trên toàn bộ codebase
test:
	go test -v -race ./...

# 7. Dọn dẹp
clean:
	rm -rf bin/
