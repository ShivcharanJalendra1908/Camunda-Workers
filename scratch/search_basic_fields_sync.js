const fs = require('fs');
const content = fs.readFileSync('scripts/sync/sync-postgres-to-es.go', 'utf8');
const lines = content.split('\n');
for (let i = 280; i < 460; i++) {
    if (lines[i].includes('doc[')) {
        console.log(`Line ${i + 1}: ${lines[i].trim()}`);
    }
}
