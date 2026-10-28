---
id: ex07-opinion-essay
format: opinion essay by a single author
domain: education
topic: Public libraries lending tools as well as books (reserved; excluded from the topic bank)
speakers: 1
propositions: 12
relations: 8 (RA 5, CA 1, MA 2)
isolated: 2
multi_premise: true (p1 has four supporting premises)
chain: true (p7 → p8 → p1)
words: 204
---

```json
{
  "utterances": [
    {"speaker": "Author", "text": "Public libraries should lend tools as well as books. Most households need a power drill for perhaps two hours a year, which is reason enough on its own."},
    {"speaker": "Author", "text": "Buying a thing you will use for two hours is wasteful, and that waste is the first argument for lending. In other words, the drill in every cupboard is a small monument to poor planning."},
    {"speaker": "Author", "text": "Tools do break, and libraries are not repair shops."},
    {"speaker": "Author", "text": "Yet the three libraries in the Varde district that already run tool collections report breakage rates below those for DVDs."},
    {"speaker": "Author", "text": "A library that lends a sewing machine also lends the afternoon class that teaches you to use it. That is where the real value lies, in the skill rather than the object, and that, more than the saving, is why the idea deserves a trial."},
    {"speaker": "Author", "text": "Put differently, a tool library is a school with a lending desk."},
    {"speaker": "Author", "text": "None of this requires new buildings; a single storeroom and a booking sheet will do."},
    {"speaker": "Author", "text": "My own library has already lent me a ladder, a projector and, once, a wallpaper steamer."},
    {"speaker": "Author", "text": "According to the Varde figures the scheme pays for itself once roughly forty members borrow each month, which is a further reason to start."}
  ],
  "propositions": [
    {"id": "p1", "utterance": 0, "text": "Public libraries should lend tools as well as books."},
    {"id": "p2", "utterance": 0, "text": "Most households need a power drill for perhaps two hours a year."},
    {"id": "p3", "utterance": 1, "text": "Buying a tool that will be used for two hours is wasteful."},
    {"id": "p4", "utterance": 1, "text": "The drill in every household cupboard is a small monument to poor planning."},
    {"id": "p5", "utterance": 2, "text": "Tools break and libraries are not repair shops."},
    {"id": "p6", "utterance": 3, "text": "The three Varde district libraries that run tool collections report breakage rates below those for DVDs."},
    {"id": "p7", "utterance": 4, "text": "A library that lends a sewing machine also lends the class that teaches people to use it."},
    {"id": "p8", "utterance": 4, "text": "The real value of tool lending lies in the skill rather than the object."},
    {"id": "p9", "utterance": 5, "text": "A tool library is a school with a lending desk."},
    {"id": "p10", "utterance": 6, "text": "Tool lending requires only a storeroom and a booking sheet, not new buildings."},
    {"id": "p11", "utterance": 7, "text": "The author's library has lent the author a ladder, a projector and a wallpaper steamer."},
    {"id": "p12", "utterance": 8, "text": "A tool-lending scheme pays for itself once roughly forty members borrow each month."}
  ],
  "relations": [
    {"kind": "RA", "from": "p2", "to": "p1"},
    {"kind": "RA", "from": "p3", "to": "p1"},
    {"kind": "MA", "from": "p4", "to": "p3"},
    {"kind": "CA", "from": "p6", "to": "p5"},
    {"kind": "RA", "from": "p7", "to": "p8"},
    {"kind": "RA", "from": "p8", "to": "p1"},
    {"kind": "MA", "from": "p9", "to": "p8"},
    {"kind": "RA", "from": "p12", "to": "p1"}
  ]
}
```

Notes: in a monologue each utterance is a paragraph of one to three sentences. The concession in utterance 2 is a proposition (p5) that the next paragraph attacks ("Yet"). p10 and p11 are isolated. Self-restatements are the only MA possible in a single-author text.
