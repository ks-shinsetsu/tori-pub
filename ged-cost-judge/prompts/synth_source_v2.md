You write short, realistic English discussions together with their argument structure.

You will be given a format, a topic (a concrete question), a list of speaker names to use, and numeric targets: the number of propositions, how many relations of each kind (RA / CA / MA), how many propositions must stay unrelated to any other, and whether the discussion must contain a conclusion with several supporting premises and a chain of at least three linked propositions. Write the discussion and annotate it.

Requirements:

- `utterances`: the discussion in order. Each utterance has a `speaker` (one of the given names, or a role such as "Host" where the format has one) and its `text`. Write natural spoken or written English for the given format: a broadcast debate has a host who asks questions and sums up, panellists who interrupt and disagree, and sometimes an audience member; a meeting has an agenda item and a chair; a forum thread has handles and short posts; an essay has paragraphs by one author. Keep each utterance to one to three sentences. Hosts, interviewers and chairs mostly ask and steer; their questions and moderation are not propositions.
- `propositions`: every claim made in the discussion, with an `id` ("p1", "p2", ...), the index of the `utterance` (0-based) that states it, and its `text` as a self-contained declarative sentence (resolve pronouns and name the speaker where needed). Every proposition is stated in exactly one utterance; an utterance states at most two propositions. Questions, greetings and moderation are not propositions. Match the target number of propositions exactly.
- `relations`: how propositions relate. `kind` is "RA" (the `from` proposition is offered as a reason supporting the `to` proposition), "CA" (the `from` proposition attacks or objects to the `to` proposition) or "MA" (the `from` proposition restates or rephrases the `to` proposition; for CA and MA, `from` is the later proposition). At most one relation between any pair of propositions. Match the target count of each kind within ±1.
- **Every relation must be clearly signalled in the text**, so that a careful reader could recover it from the text alone: support by words such as "because", "so", "that is why", "which is another reason"; an attack by "but", "except that", "though", "that ignores"; a restatement by "in other words", "that is to say", "so you are saying", "put another way". A relation that a reader would have to guess at does not count.
- Exactly the target number of propositions must have **no relation at all** (asides, factual remarks, personal notes). Spread them through the discussion; do not put them all at the end.
- Where the format has several speakers, at least half of the MA relations must be one speaker restating *another* speaker (a host summing up a panellist, a reply beginning "so you are saying").
- If a conclusion with several premises is requested, one proposition must be supported by at least two others. If a chain is requested, there must be three propositions linked p → q → r.
- Use the exact speaker names given and the exact number of speakers.

About the examples that follow: they show the **format, length and how relations are signalled**. They are not templates for content.

- Do not reuse their topics, settings, place names, institutions, speaker names, numbers or distinctive phrases. Your discussion must be about the topic you are given and nothing in it may be traceable to an example.
- Do not copy their sentence patterns in sequence (for example "X. Because of that, Y." followed by "But Z"). Vary where in an utterance the signalling words go and which ones you use; the lists above are illustrations, not a vocabulary to cycle through.
- Do not reproduce their proportions mechanically; follow the numeric targets you are given, which differ from the examples.

Examples:

{{exemplars}}
