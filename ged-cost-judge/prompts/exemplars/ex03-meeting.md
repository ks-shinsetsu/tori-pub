---
id: ex03-meeting
format: meeting
domain: business management
topic: Pay-what-you-can pricing for Tuesday performances at a regional theatre (reserved; excluded from the topic bank)
speakers: 3 (chair + 2 staff)
propositions: 11
relations: 8 (RA 5, CA 1, MA 2)
isolated: 2
multi_premise: true (p4 has two supporting premises)
chain: true (p1 → p2 → p4)
words: 166
reserved_names: Corn Exchange
---

```json
{
  "utterances": [
    {"speaker": "Ingrid", "text": "Item three: pay-what-you-can pricing for Tuesday performances. Tomasz, you looked at the numbers."},
    {"speaker": "Tomasz", "text": "Tuesday houses average thirty-eight percent full. Because the seats are empty anyway, a pay-what-you-can night costs us almost nothing in lost revenue."},
    {"speaker": "Adaeze", "text": "It also brings in people who have never been here, which is another reason to do it."},
    {"speaker": "Ingrid", "text": "So the proposal is that we run pay-what-you-can on Tuesdays from September."},
    {"speaker": "Tomasz", "text": "The risk, though, is that regulars switch to Tuesdays and pay less. Our members already skew towards midweek, which is why I worry."},
    {"speaker": "Adaeze", "text": "In other words, we would be giving a discount to the people who would pay anyway."},
    {"speaker": "Ingrid", "text": "Noted. Separately, the Tuesday bar rota is already thin."},
    {"speaker": "Tomasz", "text": "The Corn Exchange tried this and saw Tuesday attendance double in its first season, which supports Adaeze's point about new audiences."},
    {"speaker": "Adaeze", "text": "Put simply, empty seats cost us more than cheap ones."},
    {"speaker": "Ingrid", "text": "Let us take a vote at the end. One more thing: we need to tell the funders before the brochure goes to print."}
  ],
  "propositions": [
    {"id": "p1", "utterance": 1, "text": "Tuesday performances average thirty-eight percent of seats sold."},
    {"id": "p2", "utterance": 1, "text": "A pay-what-you-can Tuesday would cost the theatre almost nothing in lost revenue."},
    {"id": "p3", "utterance": 2, "text": "Pay-what-you-can pricing brings in people who have never visited the theatre."},
    {"id": "p4", "utterance": 3, "text": "The theatre should run pay-what-you-can pricing on Tuesdays from September."},
    {"id": "p5", "utterance": 4, "text": "Regular patrons would switch to Tuesdays and pay less."},
    {"id": "p6", "utterance": 4, "text": "The theatre's members already skew towards midweek attendance."},
    {"id": "p7", "utterance": 5, "text": "Pay-what-you-can pricing would give a discount to people who would pay full price anyway."},
    {"id": "p8", "utterance": 6, "text": "The Tuesday bar staff rota is already thin."},
    {"id": "p9", "utterance": 7, "text": "The Corn Exchange saw Tuesday attendance double in the first season of pay-what-you-can pricing."},
    {"id": "p10", "utterance": 8, "text": "Empty seats cost the theatre more than cheaply sold ones."},
    {"id": "p11", "utterance": 9, "text": "The funders need to be told before the brochure goes to print."}
  ],
  "relations": [
    {"kind": "RA", "from": "p1", "to": "p2"},
    {"kind": "RA", "from": "p2", "to": "p4"},
    {"kind": "RA", "from": "p3", "to": "p4"},
    {"kind": "CA", "from": "p5", "to": "p4"},
    {"kind": "RA", "from": "p6", "to": "p5"},
    {"kind": "MA", "from": "p7", "to": "p5"},
    {"kind": "RA", "from": "p9", "to": "p3"},
    {"kind": "MA", "from": "p10", "to": "p2"}
  ]
}
```

Notes: the conclusion p4 is stated *after* two of its premises (signalled by "So"). Both MAs are by a different speaker. "Let us take a vote at the end" is moderation, not a proposition. p8 and p11 are isolated.
