# Checks and live networking

Accept the license to run the machine checks. Failures block **Next** on this
page; warnings do not.

| Check | What it means |
| --- | --- |
| Install Target | A disk other than the running installer is available. Capacity is checked when you choose a storage layout. |
| Virtualization | VMM support is available. A warning means Sylve's virtual machines may be unavailable, not that installation is blocked. |
| Memory | At least 4 GiB RAM is recommended; lower memory produces a warning. |
| Live Network | An IPv4 address and default route are available. |
| Package Repositories | The package host responds over HTTPS. |
| Sylve Repositories | GitHub responds over HTTPS. |
| Offline Payload | Bundled packages and a Sylve binary are present; shown only when available. |

With an offline payload, network/repository failures become warnings and the
installer can use the bundled copies. Online sources are preferred when available.

Checks do not verify exact package availability or guarantee a successful
installation. Package downloads occur after disk preparation. These are wizard
checks, not an API enforcement boundary; the engine separately validates storage
plans before writing to disks.

## Live-network configuration

If DHCP fails, select **Configure Live Network** on the network row. Choose an
Ethernet interface and enter:

- An IPv4 address in CIDR notation, such as `192.168.1.20/24`. Prefixes `/1`
  through `/30` are supported; network and broadcast addresses are rejected.
- A different usable gateway address in the same subnet.
- One to three IPv4 DNS servers.

Interfaces can be configured even when down or unaddressed. This dialog does not
configure IPv6, VLANs, off-subnet gateways or the installed system's Network plan.

Applying settings stops DHCP on the selected interface and updates its address,
route and DNS. The installer attempts to restore the previous settings on failure
and reports incomplete recovery. Changes are refused while an installation is
running.

A remote browser may disconnect when the address changes. Reconnect to
`http://<new-live-IP>:8080`, or use the physical/IPMI/hypervisor console. After a
successful change, the installer refreshes the address/QR code and reruns checks.

API clients use `GET /api/network/live` to inspect current settings and
`POST /api/network/live` to apply them. Both require an authenticated session.

## Remote access

Open the URL shown beside the kiosk QR code and enter the installer access
password. On a text console, run `sylve-installerd -show-access` to display it.
The QR contains only the URL, not credentials.

The password is two random words followed by two digits. It is separate from the
root, Sylve administrator and GELI passwords. It changes whenever the installer
daemon starts, invalidating previous browser sessions. Remote sign-in permits ten
attempts per minute; wait a minute if throttled.

Use an isolated provisioning network or an encrypted tunnel: the live web
installer uses HTTP. Live SSH and in-guest VNC are disabled. For API access, sign
in with `POST /api/access` and retain the returned session cookie. Do not share
the kiosk's private bootstrap URL or session cookies.
