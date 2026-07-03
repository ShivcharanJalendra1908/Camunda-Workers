const fs = require('fs');
const content = fs.readFileSync('data/postgres/v2-data/associations.csv', 'utf8');
const lines = content.split('\n');
let count = 0;
for (let i = 1; i < lines.length; i++) {
    const cols = lines[i].split(',');
    if (cols.length > 2 && cols[2] !== '') {
        count++;
        if (count < 10) console.log(lines[i]);
    }
}
console.log('Total non-empty sectors:', count);
