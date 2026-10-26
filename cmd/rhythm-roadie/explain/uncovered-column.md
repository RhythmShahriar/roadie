# uncovered-column: a column that looks personal has no rule (medium)

What: A text column no profile or columns: rule covers, whose name looks
personal (email, phone, name, address, ip, dob and similar) or whose sampled
values are 20% or more emails or phones.

Why it matters: Roadie only rewrites what a rule covers. An unknown table with
customer data would reach the copy as it is.

What Roadie does: copy and prepare stop before writing anything (exit 1)
unless --allow-uncovered; plan lists each column.

By hand: Add the column under columns: in .roadie/config.yaml with its rule,
e.g. table: wp_loyalty_members, column: email, rule: email. rhythm-roadie
config init --from <source> suggests the entries.

