#!/bin/bash
#
# SingBox Manager - One-Click Installation Script
# Supports: install, uninstall, update, status
#
set -e

# ==================== Configuration ====================
VERSION="1.0.1"
INSTALL_DIR="/usr/local/bin"
DATA_DIR="/var/lib/singboxA"
SERVICE_NAME="singboxA"
WEB_PORT=3333
REPO_URL="https://github.com/dalei1563/singboxA"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# ==================== Helper Functions ====================
log_info()  { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }
log_step()  { echo -e "${BLUE}[STEP]${NC} $1"; }

check_root() {
    if [ "$EUID" -ne 0 ]; then
        log_error "Please run as root (sudo $0)"
        exit 1
    fi
}

detect_arch() {
    ARCH=$(uname -m)
    case $ARCH in
        x86_64)  SINGBOX_ARCH="amd64" ;;
        aarch64) SINGBOX_ARCH="arm64" ;;
        armv7l)  SINGBOX_ARCH="armv7" ;;
        *)
            log_error "Unsupported architecture: $ARCH"
            exit 1
            ;;
    esac
    log_info "Architecture: $SINGBOX_ARCH"
}

check_command() {
    command -v "$1" &> /dev/null
}

# ==================== sing-box Installation ====================
# sing-box version bundled with this installer
BUNDLED_SINGBOX_VERSION="1.13.7"
BUNDLED_SINGBOX_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

install_singbox() {
    log_step "Installing sing-box..."

    local src_file="${BUNDLED_SINGBOX_DIR}/sing-box-${BUNDLED_SINGBOX_VERSION}-linux-${SINGBOX_ARCH}.tar.gz"

    # Check if bundled tarball exists
    if [ -f "${src_file}" ]; then
        log_info "Using bundled sing-box: ${BUNDLED_SINGBOX_VERSION}"
        tar -xzf "$src_file" -C "$INSTALL_DIR/" sing-box-${BUNDLED_SINGBOX_VERSION}-linux-${SINGBOX_ARCH}/sing-box
        mv "$INSTALL_DIR/sing-box-${BUNDLED_SINGBOX_VERSION}-linux-${SINGBOX_ARCH}/sing-box" "$INSTALL_DIR/sing-box"
        rm -rf "$INSTALL_DIR/sing-box-${BUNDLED_SINGBOX_VERSION}-linux-${SINGBOX_ARCH}"
        chmod +x "$INSTALL_DIR/sing-box"

        # Set capabilities for TUN
        setcap cap_net_admin,cap_net_bind_service=+ep "$INSTALL_DIR/sing-box" 2>/dev/null || true

        log_info "sing-box installed: ${BUNDLED_SINGBOX_VERSION}"
    else
        log_error "Bundled sing-box not found: ${src_file}"
        log_error "Please ensure sing-box-${BUNDLED_SINGBOX_VERSION}-linux-${SINGBOX_ARCH}.tar.gz exists in:"
        log_error "  ${BUNDLED_SINGBOX_DIR}/"
        return 1
    fi
}

# ==================== singboxA Installation ====================
build_client() {
    log_step "Building singboxA..."

    if ! check_command go; then
        log_error "Go is not installed. Please install Go 1.21+"
        log_info "Install with: sudo apt install golang-go"
        exit 1
    fi

    local project_dir
    project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

    cd "$project_dir"

    export GOPROXY=https://goproxy.cn,direct
    CGO_ENABLED=0 go build -ldflags="-s -w" -o "$INSTALL_DIR/singboxA" .
    chmod +x "$INSTALL_DIR/singboxA"

    log_info "singboxA built successfully"
}

create_service() {
    log_step "Creating systemd service..."

    cat > /etc/systemd/system/${SERVICE_NAME}.service << EOF
[Unit]
Description=SingBoxA Manager
Documentation=https://github.com/dalei1563/singboxA
After=network.target network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
Environment=SINGBOX_DATA_DIR=${DATA_DIR}
ExecStart=${INSTALL_DIR}/singboxA
Restart=on-failure
RestartSec=5
LimitNOFILE=65535

# Security hardening
NoNewPrivileges=false
ProtectSystem=false
ProtectHome=false
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF

    systemctl daemon-reload
    log_info "Service created: ${SERVICE_NAME}"
}

create_directories() {
    log_step "Creating directories..."
    mkdir -p "$DATA_DIR"/{subscriptions,singbox}
    log_info "Data directory: $DATA_DIR"
}

# ==================== Main Commands ====================
do_install() {
    echo ""
    echo -e "${GREEN}================================${NC}"
    echo -e "${GREEN}  SingBox Manager Installation  ${NC}"
    echo -e "${GREEN}================================${NC}"
    echo ""

    check_root
    detect_arch

    # Install sing-box from bundled
    install_singbox

    # Build client
    build_client

    # Setup
    create_directories
    create_service

    # Start service
    read -p "Start service now? [Y/n] " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Nn]$ ]]; then
        systemctl enable ${SERVICE_NAME}
        systemctl start ${SERVICE_NAME}
        sleep 2

        if systemctl is-active --quiet ${SERVICE_NAME}; then
            log_info "Service started successfully"
        else
            log_warn "Service may not have started correctly"
            log_info "Check logs: journalctl -u ${SERVICE_NAME} -n 50"
        fi
    fi

    echo ""
    echo -e "${GREEN}========== Installation Complete ==========${NC}"
    echo ""
    echo "  Web Interface:  http://localhost:${WEB_PORT}"
    echo "  Data Directory: ${DATA_DIR}"
    echo ""
    echo "  Commands:"
    echo "    Start:   sudo systemctl start ${SERVICE_NAME}"
    echo "    Stop:    sudo systemctl stop ${SERVICE_NAME}"
    echo "    Status:  sudo systemctl status ${SERVICE_NAME}"
    echo "    Logs:    sudo journalctl -u ${SERVICE_NAME} -f"
    echo ""
    echo "  Quick Start:"
    echo "    1. Open http://localhost:${WEB_PORT}"
    echo "    2. Add subscription URL"
    echo "    3. Click Start to connect"
    echo ""
}

do_uninstall() {
    echo ""
    echo -e "${YELLOW}================================${NC}"
    echo -e "${YELLOW}  SingBox Manager Uninstall     ${NC}"
    echo -e "${YELLOW}================================${NC}"
    echo ""

    check_root

    read -p "Remove singboxA completely? [y/N] " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        log_info "Cancelled"
        exit 0
    fi

    log_step "Stopping services..."
    systemctl stop ${SERVICE_NAME} 2>/dev/null || true
    systemctl disable ${SERVICE_NAME} 2>/dev/null || true

    log_step "Removing files..."
    rm -f /etc/systemd/system/${SERVICE_NAME}.service
    rm -f "$INSTALL_DIR/singboxA"

    systemctl daemon-reload

    read -p "Remove data directory ($DATA_DIR)? [y/N] " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        rm -rf "$DATA_DIR"
        log_info "Data directory removed"
    fi

    read -p "Remove sing-box? [y/N] " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        rm -f "$INSTALL_DIR/sing-box"
        log_info "sing-box removed"
    fi

    log_info "Uninstall complete"
}

do_update() {
    echo ""
    echo -e "${BLUE}================================${NC}"
    echo -e "${BLUE}  SingBox Manager Update        ${NC}"
    echo -e "${BLUE}================================${NC}"
    echo ""

    check_root
    detect_arch

    # Reinstall sing-box from bundled
    log_step "Reinstalling sing-box from bundled..."
    install_singbox

    # Rebuild client
    log_step "Rebuilding singboxA..."
    systemctl stop ${SERVICE_NAME} 2>/dev/null || true
    build_client
    systemctl start ${SERVICE_NAME}

    log_info "Update complete"
}

do_status() {
    echo ""
    echo -e "${BLUE}================================${NC}"
    echo -e "${BLUE}  SingBox Manager Status        ${NC}"
    echo -e "${BLUE}================================${NC}"
    echo ""

    # Check singboxA
    if check_command singboxA; then
        echo -e "singboxA: ${GREEN}installed${NC}"
    else
        echo -e "singboxA: ${RED}not installed${NC}"
    fi

    # Check sing-box
    if check_command sing-box; then
        local ver
        ver=$(sing-box version 2>/dev/null | head -n1)
        echo -e "sing-box: ${GREEN}$ver${NC}"
    else
        echo -e "sing-box: ${RED}not installed${NC}"
    fi

    # Check service
    if systemctl is-active --quiet ${SERVICE_NAME}; then
        echo -e "Service: ${GREEN}running${NC}"
    else
        echo -e "Service: ${RED}stopped${NC}"
    fi

    # Check API
    if curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:${WEB_PORT}/api/status" 2>/dev/null | grep -q "200"; then
        echo -e "API: ${GREEN}responding${NC}"

        # Get status
        local status
        status=$(curl -s "http://127.0.0.1:${WEB_PORT}/api/status" 2>/dev/null)
        if [ -n "$status" ]; then
            echo ""
            echo "Proxy Status:"
            echo "$status" | python3 -c "import sys,json; d=json.load(sys.stdin)['data']; print(f\"  State: {d.get('state','unknown')}\"); print(f\"  Nodes: {d.get('node_count',0)}\"); print(f\"  Mode: {d.get('proxy_mode','unknown')}\"); print(f\"  Selected: {d.get('selected_node','none')}\")" 2>/dev/null || true
        fi
    else
        echo -e "API: ${RED}not responding${NC}"
    fi

    echo ""
}

show_help() {
    echo "SingBox Manager - Installation Script v${VERSION}"
    echo ""
    echo "Usage: $0 [command]"
    echo ""
    echo "Commands:"
    echo "  install    Install singboxA and sing-box"
    echo "  uninstall  Remove singboxA"
    echo "  update     Update sing-box and rebuild client"
    echo "  status     Show current status"
    echo "  help       Show this help"
    echo ""
    echo "Examples:"
    echo "  sudo $0 install    # Fresh installation"
    echo "  sudo $0 update     # Update to latest"
    echo "  sudo $0 status     # Check status"
    echo ""
}

# ==================== Main ====================
case "${1:-install}" in
    install)   do_install ;;
    uninstall) do_uninstall ;;
    update)    do_update ;;
    status)    do_status ;;
    help|--help|-h) show_help ;;
    *)
        log_error "Unknown command: $1"
        show_help
        exit 1
        ;;
esac
