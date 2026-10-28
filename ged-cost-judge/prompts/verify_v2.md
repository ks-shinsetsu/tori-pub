You read a discussion and report what it says about some of its claims.

You receive a discussion as numbered utterances, a list of propositions (claims) with labels C1, C2, …, and possibly a list of numbered pairs of those propositions. Judge from the text of the discussion only, as a careful reader would. Do not guess what the author meant to write.

- `propositions`: for every listed proposition, how the discussion states it, compared with the text given for it:
  - "same": the discussion states this claim with the same content.
  - "paraphrase": the discussion states this claim in different words with the same meaning.
  - "changed": the discussion states a claim in this place, but with a different content (a different claim, a stronger or weaker one, a different scope).
  - "negated": the discussion states the opposite claim (reversed polarity or quantifier, for example "should" against "should not", "all" against "few").
  - "absent": the discussion does not state this claim at all.
- `pairs`: for every listed pair, the relation the discussion states between the two propositions:
  - "RA": one is offered as a reason for the other (direction: from the reason to the claim it supports).
  - "CA": one attacks or objects to the other (direction: from the attacking claim to the attacked one).
  - "MA": one restates or rephrases the other (direction: from the restating, later claim to the claim it restates).
  - "none": the text does not present them as related. A relation counts only if the text signals it clearly enough that a careful reader would recover it without guessing; two claims that merely appear near each other are "none".
  - `direction` is "first_to_second" if the relation goes from the first proposition of the pair to the second, "second_to_first" if the other way round, and "none" when the relation is "none".
- `fluency`: how well the discussion reads as text, from "1" (broken: ungrammatical, dangling references, garbled) to "5" (fluent). Judge the language, not the argument.
- `coherence`: how well the argument hangs together, from "1" (the claims do not fit together) to "5" (fully coherent). An argument can be fluent but incoherent; rate the two separately.
- `note`: one short sentence on anything that made a judgment hard (empty if nothing).
