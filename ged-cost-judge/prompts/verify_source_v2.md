You annotate the argument structure of a discussion, following the definitions below.

You receive a discussion as numbered utterances and the list of propositions (claims) it contains, with labels P1, P2, …. Judge from the text of the discussion only, as a careful reader would.

Definitions (Inference Anchoring Theory, IAT). They fix how the three kinds of relation are told apart.

- Locution: what a speaker actually says in an utterance.
- Proposition: the content of a locution, written out as one self-contained claim (pronouns resolved, ellipsis filled in). Questions, greetings and moderation carry no proposition.
- A relation between two propositions X and Y exists only where it is anchored in the discussion: in going from one locution to another, within a turn or in replying to another speaker, a speaker performs one of the three moves below on the two contents. Two contents that could be connected but that no speaker connects are not related.
  - RA (inference), anchored by arguing: the speaker offers X as a ground for accepting Y. The move can be read back as "Y, because X". Direction: from the ground X to the claim Y, whichever is said first.
  - CA (conflict), anchored by disagreeing: the speaker sets X against Y, as incompatible with it or as an objection to it. The move can be read back as "Y? No, or not quite: X". Direction: from the objecting claim X to the claim Y it is set against; X is the later of the two.
  - MA (rephrase), anchored by restating: the speaker gives the content of Y again as X (in other words, as a summary, or to check what was meant) and offers it neither as a ground nor as an objection. The speaker may be the one who said Y or someone else. The move can be read back as "Y; in other words, X". Direction: from the restating claim X to the claim Y; X is the later of the two.
- Moves that anchor no relation: asserting a new claim, asking, answering, agreeing or disagreeing without saying why ("I agree", "that is wrong"), moving on to another point.
- Which move it is:
  1. An explicit framing decides. "because", "since", "the reason is", "that is why", "which is another reason", "which shows" frame arguing. "but", "except that", "though", "that ignores", "on the contrary" frame disagreeing. "in other words", "that is to say", "put another way", "so you are saying", "what you mean is" frame restating.
  2. Where the framing is ambiguous or missing ("so", "and", "I mean", "exactly", two sentences side by side), the contents decide. If X only says again what Y says and brings no ground of its own, the move is restating. If X is a different claim that makes Y more acceptable, it is arguing. If X is a different claim that makes Y less acceptable, it is disagreeing. If none of these can be read without guessing, there is no relation.
  3. An example or a special case of Y, or something that follows from Y, is a different claim from Y. Under rule 2 the move between them is arguing (the example supports Y; what follows from Y is supported by Y), never restating.
- At most one relation between two propositions.

Output:

- `propositions`: for every listed proposition, how the discussion states it, compared with the text given for it: "same" (same content), "paraphrase" (different words, same meaning), "changed" (a different content), "negated" (the opposite claim: reversed polarity or quantifier), or "absent" (not stated).
- `relations`: every relation anchored in the discussion between two listed propositions, as defined above, and only those:
  - "RA": `from` is offered as a reason supporting `to`.
  - "CA": `from` attacks or objects to `to`.
  - "MA": `from` restates or rephrases `to` (for a restatement, `from` is the later of the two).
  - Include a relation only if the text anchors the move clearly enough that a careful reader would recover it without guessing (rules 1 and 2). Two claims that merely appear near each other, or that are on the same subject, are not related.
  - At most one relation between any pair of propositions. Some propositions are related to nothing; do not invent relations for them.
  - Questions, greetings and moderation are not propositions and take no part in relations.
