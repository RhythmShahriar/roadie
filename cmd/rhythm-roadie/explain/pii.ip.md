# pii.ip: a public IP address (medium)

What: A public IPv4 or IPv6 address in an IP column or an "ip" key inside
serialized or JSON data, such as WordPress session tokens.

Why it matters: An IP address is personal data under the GDPR and can locate a
visitor.

What Roadie does: IPs become documentation addresses (192.0.2.0/24,
198.51.100.0/24, 203.0.113.0/24, 2001:db8::/32).

By hand: Add the column under columns: with rule: ip.

