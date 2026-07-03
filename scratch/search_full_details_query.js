const fs = require('fs');
const path = require('path');

const dir = 'internal/workers/data-access/query-postgresql';
function scan(d) {
    fs.readdirSync(d).forEach(f => {
        const p = path.join(d, f);
        if (fs.statSync(p).isDirectory()) {
            scan(p);
        } else if (f.endsWith('.go')) {
            const content = fs.readFileSync(p, 'utf8');
            if (content.includes('franchise_full_details') || content.includes('FranchiseFullDetails')) {
                console.log(`${p}`);
            }
        }
    });
}
scan(dir);
