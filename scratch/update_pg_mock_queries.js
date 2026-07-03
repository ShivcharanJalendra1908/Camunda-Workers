const fs = require('fs');

const filePath = 'internal/workers/data-access/query-postgresql/handler_test.go';
let content = fs.readFileSync(filePath, 'utf8');

// The exact string to replace:
const target = 'SELECT id, name, description, investment_min, investment_max, category, locations, is_verified, created_at, updated_at FROM franchises WHERE id = \\$1';
const replacement = 'SELECT l\\.id, l\\.name, l\\.description.*FROM listings';

if (content.includes(target)) {
    content = content.split(target).join(replacement);
    fs.writeFileSync(filePath, content);
    console.log('Successfully replaced all mock query strings!');
} else {
    console.error('Target string not found in file!');
}
