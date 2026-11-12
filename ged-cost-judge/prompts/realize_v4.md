You edit a discussion so that its argument structure changes in exactly the ways requested, and in no other way.

You receive the discussion as numbered utterances, its propositions (claims) with the utterance that states each one, the relations between propositions, a list of edits, and the list of propositions whose final text you must return. The discussion may itself be an edited version of an earlier one; treat it exactly like any other discussion and change only what the edits below require.

The relations are those of the definitions below; the reader who checks your edit uses the same definitions.

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

Every edit must be visible in the text. A careful reader who sees only the edited discussion must be able to tell that the change happened:

- Relation edits change the framing that anchors the relation (the speaker's move), not the claims. Use an explicit framing of rule 1 for the move you create, so that the reader does not have to fall back on rule 2.
  - Removing a relation: the two claims stay in the discussion with their wording, but nothing may still present one as related to the other. Put them in different sentences with no framing between them, and so that side by side they no longer read as a ground, an objection or a restatement (rule 2); if they sit in the same or in adjacent utterances, you may move the premise one utterance away. Deleting only "because" is not enough when the order of the sentences still reads as "reason, then conclusion".
  - Adding a relation: add an explicit framing of the move (rule 1) that ties the first claim to the second, without changing what either claims.
  - Changing the kind of a relation: keep the wording of both propositions and replace only the framing around them with an explicit framing of the new move (rule 1). Never negate, weaken or reverse a proposition to make the new kind fit: that would be a different edit.
  - Reversing a relation: rephrase the link so that the former conclusion is now offered for the former premise, keeping what each claims.
- Content edits (paraphrase, change, negation) change the proposition's wording in the utterance that states it, and nothing else about it.
- Removing a proposition removes its wording from the discussion.
- Adding a proposition adds its wording to an utterance or to a new utterance; it must not support, attack or restate anything.

Infeasible edits. Some relation edits (changing a relation's kind, reversing a relation, adding a relation) cannot always be made without changing what a proposition claims, for example turning an objection into a reason usually requires negating it. If any requested relation edit cannot be made while keeping every proposition's claim as it is, do not force it: set `infeasible` to true, give the number of that edit (as numbered under Edits) in `infeasible_edit`, explain briefly in `reason`, and leave `changes` and `propositions` empty. Otherwise set `infeasible_edit` to 0. Use this only for those three kinds of edit and only when it is genuinely impossible; it is not a way to skip a hard edit.

Rules:

- Change only what an edit needs. You may also make minimal connective fixes (pronouns, connectives, references to an earlier utterance) in the utterance directly before or after an edited utterance, so the discussion still reads naturally. A connective fix must not change any claim or relation.
- Do not change the meaning of any proposition or relation that is not named in an edit. Every utterance you do not return is kept exactly as it is, character for character.
- Return only the utterances you change in `changes`:
  - `op` "replace": `utterance` is the index of the utterance to rewrite; `text` is its full new text; `speaker` is its speaker (usually unchanged).
  - `op` "insert_after": add a new utterance after the utterance with index `utterance` (use -1 to insert at the very beginning); give `speaker` and `text`.
  - `op` "delete": remove the utterance with index `utterance` (leave `text` and `speaker` empty).
  - `role` is "edit" for a change that carries out an edit and "connective" for a minimal connective fix.
  - Indices always refer to the numbering you were given.
- In `propositions`, return every proposition listed as one you must return, and also every other proposition stated in an utterance you changed. For each, give its `id` and either its final self-contained text (as it is now stated in the edited discussion) in `text`, or `unchanged` true if its content is exactly as given. Paraphrased, changed, negated and added propositions always need a new `text`. The text you return must describe what the edited discussion actually says; it is checked against the discussion.
- If you are told that a previous attempt was rejected, fix the problems it names.
