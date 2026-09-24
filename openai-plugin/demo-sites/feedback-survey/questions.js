// The survey, defined once and shared by the form (index.html) and the results page.
// type: "choice" (pick one), "multi" (pick any), "scale" (1-5), "text" (free text).
window.SURVEY_QUESTIONS = [
  {
    id: 'visit', type: 'choice', required: true,
    label: 'How did you visit us today?',
    options: ['Sat in', 'Took it away', 'Ordered ahead for pickup']
  },
  {
    id: 'ordered', type: 'multi', required: false,
    label: 'What did you have?',
    hint: 'Pick as many as apply.',
    options: ['Coffee or espresso', 'Tea', 'Pastry or cake', 'Breakfast', 'Lunch', 'Something else']
  },
  {
    id: 'taste', type: 'scale', required: true,
    label: 'How did everything taste?',
    low: 'Poor', high: 'Excellent'
  },
  {
    id: 'service', type: 'scale', required: true,
    label: 'How was the service?',
    low: 'Poor', high: 'Excellent'
  },
  {
    id: 'wait', type: 'choice', required: true,
    label: 'How long did you wait for your order?',
    options: ['Under 5 minutes', '5 to 10 minutes', 'More than 10 minutes']
  },
  {
    id: 'again', type: 'choice', required: true,
    label: 'Would you come back?',
    options: ['Yes', 'Maybe', 'No']
  },
  {
    id: 'improve', type: 'text', required: false,
    label: 'What is one thing we could do better?',
    hint: 'Anything at all: the menu, the music, the chairs.'
  }
];
