#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PACKAGE_NAME="singboxa"
APP_NAME="singboxA"
VERSION="${VERSION:-1.0.6}"
SINGBOX_VERSION="${SINGBOX_VERSION:-1.13.7}"
DEB_ARCH="${DEB_ARCH:-$(dpkg --print-architecture)}"

case "${DEB_ARCH}" in
    amd64)
        GOARCH_VALUE="amd64"
        SINGBOX_ARCH="amd64"
        ;;
    arm64)
        GOARCH_VALUE="arm64"
        SINGBOX_ARCH="arm64"
        ;;
    armhf)
        GOARCH_VALUE="arm"
        GOARM_VALUE="7"
        SINGBOX_ARCH="armv7"
        ;;
    *)
        echo "Unsupported Debian architecture: ${DEB_ARCH}" >&2
        exit 1
        ;;
esac

TARBALL="${ROOT_DIR}/scripts/sing-box-${SINGBOX_VERSION}-linux-${SINGBOX_ARCH}.tar.gz"
if [[ ! -f "${TARBALL}" ]]; then
    echo "Bundled sing-box tarball not found: ${TARBALL}" >&2
    exit 1
fi

BUILD_DIR="${ROOT_DIR}/build/deb"
WORK_DIR="${BUILD_DIR}/${PACKAGE_NAME}_${VERSION}_${DEB_ARCH}"
DIST_DIR="${ROOT_DIR}/dist"
EXTRACT_DIR="${BUILD_DIR}/sing-box-extract"

rm -rf "${WORK_DIR}" "${EXTRACT_DIR}"
mkdir -p \
    "${WORK_DIR}/DEBIAN" \
    "${WORK_DIR}/usr/local/bin" \
    "${WORK_DIR}/usr/local/lib/sing-box" \
    "${WORK_DIR}/usr/share/doc/${PACKAGE_NAME}" \
    "${WORK_DIR}/lib/systemd/system" \
    "${WORK_DIR}/etc/ld.so.conf.d" \
    "${DIST_DIR}" \
    "${EXTRACT_DIR}"

echo "Building ${APP_NAME} ${VERSION} for ${DEB_ARCH}..."
(
    cd "${ROOT_DIR}"
    export CGO_ENABLED=0
    export GOOS=linux
    export GOARCH="${GOARCH_VALUE}"
    if [[ -n "${GOARM_VALUE:-}" ]]; then
        export GOARM="${GOARM_VALUE}"
    fi
    go build -ldflags="-s -w" -o "${WORK_DIR}/usr/local/bin/${APP_NAME}" .
)

echo "Bundling sing-box ${SINGBOX_VERSION} for ${SINGBOX_ARCH}..."
tar -xzf "${TARBALL}" -C "${EXTRACT_DIR}"
install -m 0755 "${EXTRACT_DIR}/sing-box-${SINGBOX_VERSION}-linux-${SINGBOX_ARCH}/sing-box" \
    "${WORK_DIR}/usr/local/bin/sing-box"
install -m 0644 "${EXTRACT_DIR}/sing-box-${SINGBOX_VERSION}-linux-${SINGBOX_ARCH}/libcronet.so" \
    "${WORK_DIR}/usr/local/lib/sing-box/libcronet.so"

install -m 0644 "${ROOT_DIR}/README.md" "${WORK_DIR}/usr/share/doc/${PACKAGE_NAME}/README.md"
install -m 0644 "${ROOT_DIR}/LICENSE" "${WORK_DIR}/usr/share/doc/${PACKAGE_NAME}/copyright"

cat > "${WORK_DIR}/etc/ld.so.conf.d/singboxA.conf" <<'EOF'
/usr/local/lib/sing-box
EOF

cat > "${WORK_DIR}/lib/systemd/system/singboxA.service" <<'EOF'
[Unit]
Description=SingBoxA Manager
Documentation=https://github.com/dalei1563/singboxA
After=network.target network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
Environment=SINGBOX_DATA_DIR=/var/lib/singboxA
ExecStart=/usr/local/bin/singboxA
Restart=on-failure
RestartSec=5
LimitNOFILE=65535
NoNewPrivileges=false
ProtectSystem=false
ProtectHome=false
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF

cat > "${WORK_DIR}/DEBIAN/control" <<EOF
Package: ${PACKAGE_NAME}
Version: ${VERSION}
Section: net
Priority: optional
Architecture: ${DEB_ARCH}
Depends: libc6
Maintainer: dalei1563 <dalei1563@gmail.com>
Homepage: https://github.com/dalei1563/singboxA
Description: SingBoxA web manager with bundled sing-box core
 A proxy management tool based on sing-box with a web UI, subscription
 management, rule routing, node testing, and diagnostic log APIs.
EOF

cat > "${WORK_DIR}/DEBIAN/postinst" <<'EOF'
#!/bin/sh
set -e

case "$1" in
    configure)
        mkdir -p /var/lib/singboxA/subscriptions /var/lib/singboxA/singbox /var/lib/singboxA/logs
        chmod 755 /var/lib/singboxA /var/lib/singboxA/subscriptions /var/lib/singboxA/singbox /var/lib/singboxA/logs

        chmod 755 /usr/local/bin/singboxA /usr/local/bin/sing-box
        if command -v setcap >/dev/null 2>&1; then
            setcap cap_net_admin,cap_net_bind_service=+ep /usr/local/bin/sing-box 2>/dev/null || true
        fi
        if command -v ldconfig >/dev/null 2>&1; then
            ldconfig
        fi
        if command -v systemctl >/dev/null 2>&1; then
            systemctl daemon-reload || true
        fi

        echo "SingBoxA installed."
        echo "Start with: sudo systemctl enable --now singboxA"
        echo "Web UI: http://localhost:3333"
        ;;
esac

exit 0
EOF

cat > "${WORK_DIR}/DEBIAN/prerm" <<'EOF'
#!/bin/sh
set -e

case "$1" in
    remove|deconfigure)
        if command -v systemctl >/dev/null 2>&1; then
            systemctl stop singboxA 2>/dev/null || true
            systemctl disable singboxA 2>/dev/null || true
        fi
        ;;
esac

exit 0
EOF

cat > "${WORK_DIR}/DEBIAN/postrm" <<'EOF'
#!/bin/sh
set -e

case "$1" in
    remove|purge)
        if command -v systemctl >/dev/null 2>&1; then
            systemctl daemon-reload || true
        fi
        if command -v ldconfig >/dev/null 2>&1; then
            ldconfig
        fi
        if [ "$1" = "purge" ]; then
            echo "SingBoxA data is kept at /var/lib/singboxA."
            echo "Remove it manually if needed: sudo rm -rf /var/lib/singboxA"
        fi
        ;;
esac

exit 0
EOF

chmod 0755 "${WORK_DIR}/DEBIAN/postinst" "${WORK_DIR}/DEBIAN/prerm" "${WORK_DIR}/DEBIAN/postrm"

OUTPUT="${DIST_DIR}/${PACKAGE_NAME}_${VERSION}_${DEB_ARCH}.deb"
dpkg-deb --build --root-owner-group "${WORK_DIR}" "${OUTPUT}"

echo "Built ${OUTPUT}"
