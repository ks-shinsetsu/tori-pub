You write short, realistic English discussions together with their argument structure.

You will be given a format, a topic (a concrete question), a list of speaker names to use, and numeric targets: the number of propositions, how many relations of each kind (RA / CA / MA), how many propositions must stay unrelated to any other, and whether the discussion must contain a conclusion with several supporting premises and a chain of at least three linked propositions. Write the discussion and annotate it. The relations are those of the definitions below; the reader who checks your discussion uses the same definitions.

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

Requirements:

- `utterances`: the discussion in order. Each utterance has a `speaker` (one of the given names, or a role such as "Host" where the format has one) and its `text`. Write natural spoken or written English for the given format: a broadcast debate has a host who asks questions and sums up, panellists who interrupt and disagree, and sometimes an audience member; a meeting has an agenda item and a chair; a forum thread has handles and short posts; an essay has paragraphs by one author. Keep each utterance to one to three sentences. Hosts, interviewers and chairs mostly ask and steer; their questions and moderation are not propositions.
- `propositions`: every claim made in the discussion, with an `id` ("p1", "p2", ...), the index of the `utterance` (0-based) that states it, and its `text` as a self-contained declarative sentence (resolve pronouns and name the speaker where needed). Every proposition is stated in exactly one utterance; an utterance states at most two propositions. Questions, greetings and moderation are not propositions. Match the target number of propositions exactly.
- `relations`: how propositions relate, as defined above. `kind` is "RA" (the `from` proposition is offered as a ground for the `to` proposition), "CA" (the `from` proposition is set against the `to` proposition) or "MA" (the `from` proposition restates the `to` proposition; for CA and MA, `from` is the later proposition). At most one relation between any pair of propositions. Match the target count of each kind within ±1.
- **Every relation must be anchored in the text by a framing that identifies its move** (rule 1), so that a careful reader could recover it from the text alone. Where you use an ambiguous word such as "so", the two contents must leave no doubt which move it is (rule 2). In an MA the restating proposition carries the content of the other again; a claim that brings a new ground is an RA and must be framed as one. A relation that a reader would have to guess at does not count.
- **Propositions you annotate as unrelated must not be anchored either**: no framing between them, and contents that do not read as a ground, an objection or a restatement of a neighbouring claim (rule 2).
- Exactly the target number of propositions must have **no relation at all** (asides, factual remarks, personal notes). Spread them through the discussion; do not put them all at the end.
- Where the format has several speakers, at least half of the MA relations must be one speaker restating *another* speaker (a host summing up a panellist, a reply beginning "so you are saying").
- If a conclusion with several premises is requested, one proposition must be supported by at least two others. If a chain is requested, there must be three propositions linked p → q → r.
- A conclusion and two of its premises are three propositions, and an utterance states at most two. Do not put all three into one utterance: give the second premise in another utterance, by the same speaker later on or by another speaker. The same holds for a chain p → q → r.
- Use the exact speaker names given and the exact number of speakers.
- If you are told that a previous attempt was rejected, write a new discussion that fixes the problems named and still meets every requirement above.

About the examples that follow: they show the **format, length and how relations are anchored**. They are not templates for content.

- Do not reuse their topics, settings, place names, institutions, speaker names, numbers or distinctive phrases. Your discussion must be about the topic you are given and nothing in it may be traceable to an example.
- Do not copy their sentence patterns in sequence (for example "X. Because of that, Y." followed by "But Z"). Vary where in an utterance the framing goes and which words you use; the lists in rule 1 are illustrations, not a vocabulary to cycle through.
- Do not reproduce their proportions mechanically; follow the numeric targets you are given, which differ from the examples.

Examples:

{{exemplars}}
