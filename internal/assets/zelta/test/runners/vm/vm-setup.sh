#!/bin/bash
# Local CI VM setup script for zelta ShellSpec tests
# Run as root on a fresh Ubuntu 24.04 VM

set -euo pipefail

echo "==> Installing system packages"
apt-get update && apt-get install -y \
    zfsutils-linux \
    sudo \
    curl \
    git \
    man-db \
    openssh-server

echo "==> Enabling SSH"
sed -i 's/^#*PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config
sed -i 's/^#*PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config
# Remove cloud-init drop-in that may override password auth
rm -f /etc/ssh/sshd_config.d/60-cloudimg-settings.conf
systemctl enable --now ssh

echo "==> Installing ShellSpec"
if command -v shellspec &>/dev/null; then
    echo "    ShellSpec already installed, skipping"
else
    curl -fsSL https://git.io/shellspec | sh -s -- --yes --prefix /usr/local
fi

echo "==> Creating test user"
if id testuser &>/dev/null; then
    echo "    testuser already exists, skipping"
else
    useradd -m -s /bin/bash testuser
fi

echo "==> Configuring sudoers"
echo 'testuser ALL=(ALL) NOPASSWD: /usr/bin/dd *, /usr/bin/rm -f /tmp/*, /usr/bin/truncate *, /usr/sbin/zpool *, /usr/sbin/zfs *' \
    > /etc/sudoers.d/testuser

chown root:root /etc/sudoers.d/testuser
chmod 0440 /etc/sudoers.d/testuser
visudo -cf /etc/sudoers.d/testuser

echo "==> Creating test runner script"
cat > /home/testuser/run_test.sh << 'EOF'
#!/bin/bash
cd ~/zelta
export SANDBOX_ZELTA_SRC_POOL=apool
export SANDBOX_ZELTA_TGT_POOL=bpool
export SANDBOX_ZELTA_SRC_DS=apool/treetop
export SANDBOX_ZELTA_TGT_DS=bpool/backups
shellspec
EOF

chmod +x /home/testuser/run_test.sh
chown testuser:testuser /home/testuser/run_test.sh
echo "created test runner: /home/testuser/run_test.sh"

git clone https://github.com/bell-tower/zelta.git /home/testuser/zelta
chown -R testuser:testuser /home/testuser/zelta
echo "cloned zelta to: /home/testuser/zelta"

echo ""
echo "==> Setup complete!"
echo ""
echo "To run tests:"
echo "  su - testuser"
echo "  cd zelta"
echo "  git checkout (branchname)"
echo "  ~/run_test.sh"
