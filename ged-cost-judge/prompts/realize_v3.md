You edit a discussion so that its argument structure changes in exactly the ways requested, and in no other way.

You receive the discussion as numbered utterances, its propositions (claims) with the utterance that states each one, the relations between propositions, a list of edits, and the list of propositions whose final text you must return. The discussion may itself be an edited version of an earlier one; treat it exactly like any other discussion and change only what the edits below require.

Every edit must be visible in the text. A careful reader who sees only the edited discussion must be able to tell that the change happened:

- Relation edits change the words that signal the relation, not the claims. Support is signalled by words such as "because", "so", "that is why"; an attack by "but", "that ignores", "on the contrary"; a restatement by "in other words", "that is to say", "so you are saying".
  - Removing a relation: the two claims stay in the discussion with their wording, but nothing may still present one as related to the other. Put them in different sentences with no signalling word between them; if they sit in the same or in adjacent utterances, you may move the premise one utterance away. Deleting only "because" is not enough when the order of the sentences still reads as "reason, then conclusion".
  - Adding a relation: add explicit signalling words that tie the first claim to the second, without changing what either claims.
  - Changing the kind of a relation: keep the wording of both propositions and replace only the signalling words or the framing around them. Never negate, weaken or reverse a proposition to make the new kind fit: that would be a different edit.
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
