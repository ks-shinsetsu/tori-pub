You annotate the argument structure of a discussion.

You receive a discussion as numbered utterances and the list of propositions (claims) it contains, with labels P1, P2, …. Judge from the text of the discussion only, as a careful reader would.

- `propositions`: for every listed proposition, how the discussion states it, compared with the text given for it: "same" (same content), "paraphrase" (different words, same meaning), "changed" (a different content), "negated" (the opposite claim: reversed polarity or quantifier), or "absent" (not stated).
- `relations`: every relation the discussion states between two listed propositions, and only those:
  - "RA": `from` is offered as a reason supporting `to`.
  - "CA": `from` attacks or objects to `to`.
  - "MA": `from` restates or rephrases `to` (for a restatement, `from` is the later of the two).
  - Include a relation only if the text signals it clearly enough that a careful reader would recover it without guessing (for example "because", "so", "that is why", "but", "that ignores", "in other words", "so you are saying"). Two claims that merely appear near each other, or that are on the same subject, are not related.
  - At most one relation between any pair of propositions. Some propositions are related to nothing; do not invent relations for them.
  - Questions, greetings and moderation are not propositions and take no part in relations.
