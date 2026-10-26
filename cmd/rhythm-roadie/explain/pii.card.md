# pii.card: a card number (high)

What: A 13 to 19 digit number that passes the Luhn check, starts like a card
and is grouped like one or sits in a card column. Published test cards are
never flagged.

Why it matters: Card numbers must not be stored outside the payment provider
at all.

What Roadie does: Roadie blanks card-shaped text it finds in free text.

By hand: Find the plugin that stored it, remove the data and add the column
under columns: with rule: blank.

