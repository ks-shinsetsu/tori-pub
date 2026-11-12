You read a discussion and report what it says about some of its claims, following the definitions below.

You receive a discussion as numbered utterances, a list of propositions (claims) with labels C1, C2, …, and possibly a list of numbered pairs of those propositions. Judge from the text of the discussion only, as a careful reader would. Do not guess what the author meant to write.

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

- `propositions`: for every listed proposition, how the discussion states it, compared with the text given for it:
  - "same": the discussion states this claim with the same content.
  - "paraphrase": the discussion states this claim in different words with the same meaning.
  - "changed": the discussion states a claim in this place, but with a different content (a different claim, a stronger or weaker one, a different scope).
  - "negated": the discussion states the opposite claim (reversed polarity or quantifier, for example "should" against "should not", "all" against "few").
  - "absent": the discussion does not state this claim at all.
- `pairs`: for every listed pair, the relation anchored in the discussion between the two propositions, as defined above:
  - "RA": one is offered as a reason for the other (direction: from the reason to the claim it supports).
  - "CA": one attacks or objects to the other (direction: from the attacking claim to the attacked one).
  - "MA": one restates or rephrases the other (direction: from the restating, later claim to the claim it restates).
  - "none": the text does not present them as related. A relation counts only if the text anchors the move clearly enough that a careful reader would recover it without guessing (rules 1 and 2); two claims that merely appear near each other are "none".
  - `direction` is "first_to_second" if the relation goes from the first proposition of the pair to the second, "second_to_first" if the other way round, and "none" when the relation is "none".
- `fluency`: how well the discussion reads as text, from "1" (broken: ungrammatical, dangling references, garbled) to "5" (fluent). Judge the language, not the argument.
- `coherence`: how well the argument hangs together, from "1" (the claims do not fit together) to "5" (fully coherent). An argument can be fluent but incoherent; rate the two separately.
- `note`: one short sentence on anything that made a judgment hard (empty if nothing).
