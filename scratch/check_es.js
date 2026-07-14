const { Client } = require('@elastic/elasticsearch');

const client = new Client({
    node: 'http://localhost:9200', // Update if necessary
});

async function checkRecords() {
    try {
        // Check for Real Estate in Mumbai
        const q1 = await client.search({
            index: 'associations',
            body: {
                query: {
                    bool: {
                        must: [
                            { match: { "industry.name": "Real Estate" } }
                        ],
                        filter: [
                            { term: { "location.city": "mumbai" } }
                        ]
                    }
                }
            }
        });
        console.log("Real estate in Mumbai:", q1.body.hits.total.value);

        // Check for Automobile
        const q2 = await client.search({
            index: 'associations',
            body: {
                query: {
                    bool: {
                        must: [
                            { match: { "industry.name": "Automobile" } }
                        ]
                    }
                }
            }
        });
        console.log("Automobile:", q2.body.hits.total.value);
    } catch (e) {
        console.error(e.message);
    }
}

checkRecords();
