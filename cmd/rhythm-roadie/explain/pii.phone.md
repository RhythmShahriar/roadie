# pii.phone: a real-looking phone number (high)

What: A phone number in a phone-like column, or a strong-format number in free
text (a + and country code, a tel: link or a national format with separators),
outside the fictional ranges Roadie uses.

Why it matters: A phone number identifies and reaches a person; SMS plugins on
a staging copy can text them.

What Roadie does: Phones become numbers in Echo's test range (+1 555 01xx) or,
with phone_style: country, the country's fictional range.

By hand: Add the column under columns: with rule: phone, or allow a business
number with --allow-phones.

