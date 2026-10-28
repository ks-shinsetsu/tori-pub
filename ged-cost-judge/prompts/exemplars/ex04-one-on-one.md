---
id: ex04-one-on-one
format: one-on-one argument
domain: daily life
topic: Whether two flatmates should get rid of their television (reserved; excluded from the topic bank)
speakers: 2
propositions: 10
relations: 7 (RA 4, CA 1, MA 2)
isolated: 1
multi_premise: true (p1 has two supporting premises)
chain: true (p5 → p6 → p1)
words: 118
---

```json
{
  "utterances": [
    {"speaker": "Ruth", "text": "I think we should get rid of the television. After all, neither of us has switched it on since March."},
    {"speaker": "Kwame", "text": "But it is the only screen big enough for when your parents visit and want the football."},
    {"speaker": "Ruth", "text": "Fair point. In other words, it earns its place a few weekends a year."},
    {"speaker": "Kwame", "text": "Go on, though. You had more."},
    {"speaker": "Ruth", "text": "It fills half the wall, so the room would feel twice the size without it, and that alone is a reason to let it go."},
    {"speaker": "Kwame", "text": "We could move it to the spare room instead, because that room gets no light anyway."},
    {"speaker": "Ruth", "text": "So what you are saying is keep it but hide it."},
    {"speaker": "Kwame", "text": "Exactly. And frankly I would miss the Sunday films."}
  ],
  "propositions": [
    {"id": "p1", "utterance": 0, "text": "Ruth and Kwame should get rid of their television."},
    {"id": "p2", "utterance": 0, "text": "Neither Ruth nor Kwame has switched the television on since March."},
    {"id": "p3", "utterance": 1, "text": "The television is the only screen big enough for Ruth's parents to watch football on when they visit."},
    {"id": "p4", "utterance": 2, "text": "The television earns its place a few weekends a year."},
    {"id": "p5", "utterance": 4, "text": "The television fills half the wall."},
    {"id": "p6", "utterance": 4, "text": "The room would feel twice the size without the television."},
    {"id": "p7", "utterance": 5, "text": "The television could be moved to the spare room."},
    {"id": "p8", "utterance": 5, "text": "The spare room gets no natural light."},
    {"id": "p9", "utterance": 6, "text": "Kwame proposes keeping the television but hiding it in the spare room."},
    {"id": "p10", "utterance": 7, "text": "Kwame would miss the Sunday films."}
  ],
  "relations": [
    {"kind": "RA", "from": "p2", "to": "p1"},
    {"kind": "CA", "from": "p3", "to": "p1"},
    {"kind": "MA", "from": "p4", "to": "p3"},
    {"kind": "RA", "from": "p5", "to": "p6"},
    {"kind": "RA", "from": "p6", "to": "p1"},
    {"kind": "RA", "from": "p8", "to": "p7"},
    {"kind": "MA", "from": "p9", "to": "p7"}
  ]
}
```

Notes: a two-person argument is naturally attack-heavy; this exemplar keeps one CA by letting Ruth *restate* Kwame's objection (MA) rather than counter it. Utterance 3 contains no proposition. p10 is isolated.
