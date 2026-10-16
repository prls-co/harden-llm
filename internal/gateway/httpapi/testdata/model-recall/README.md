# Model Recall consumer contract fixtures

Source: `prls-co/masked-recall`, commit `6a3e5ec`, canonical `contracts/schemas/llm/`.
These are deterministic test inputs; Model Recall remains their production owner.

- `preparation-v2.schema.json`: SHA-256 `34427ef23043df99a2160f5c69b1f4a6c5197a31577eca05ffea57329195758b`.
- `entity-association-v5.schema.json`: SHA-256 `c8215677501c0f2265d03a7cca9c419847081c68dbd2bab35f2b0c7df0411d7f`.

The TLS stub exercises the actual gateway/provider code and returns synthetic model text. It provides no evidence about live inference quality.
