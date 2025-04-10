import http from 'k6/http';
import { check, sleep } from 'k6';
import { Trend } from 'k6/metrics';
import { SharedArray } from 'k6/data';

// Endpoint-specific metrics
const staticFileDuration = new Trend('static_file_duration');
const articleListDuration = new Trend('article_list_duration');
const singleArticleDuration = new Trend('single_article_duration');
const rssFeedDuration = new Trend('rss_feed_duration');

// Define test profiles
const testProfiles = {
  gentle: {
    stages: [
      { duration: '30s', target: 20 }, // Ramp up to 20 users over 30 seconds
      { duration: '1m', target: 20 },  // Stay at 20 users for 1 minute
      { duration: '30s', target: 0 },  // Ramp down to 0 users
    ],
    sleepBetweenRequests: { min: 1, max: 2 },
    thresholds: {
      http_req_duration: ['p(95)<500'], // 95% of requests should complete within 500ms
    }
  },
  moderate: {
    stages: [
      { duration: '30s', target: 50 },  // Ramp up to 50 users
      { duration: '2m', target: 50 },   // Stay at 50 users for 2 minutes
      { duration: '30s', target: 0 },   // Ramp down
    ],
    sleepBetweenRequests: { min: 0.5, max: 1.5 },
    thresholds: {
      http_req_duration: ['p(95)<750'], // More lenient threshold
    }
  },
  harsh: {
    stages: [
      { duration: '20s', target: 100 }, // Quickly ramp up to 100 users
      { duration: '1m', target: 200 },  // Ramp up to 200 users over 1 minute
      { duration: '2m', target: 200 },  // Stay at 200 users for 2 minutes
      { duration: '30s', target: 0 },   // Ramp down
    ],
    sleepBetweenRequests: { min: 0.1, max: 0.5 },
    thresholds: {
      http_req_duration: ['p(95)<1000', 'p(99)<2000'], // More lenient thresholds
    }
  },
  violence: {
    stages: [
      { duration: '500s', target: 500 },
      { duration: '10s', target: 0 },
    ],
    sleepBetweenRequests: { min: 0.1, max: 0.5 },
    thresholds: {
      http_req_duration: ['p(95)<1000', 'p(99)<2000'], // More lenient thresholds
    }
  },
  spike: {
    stages: [
      { duration: '10s', target: 30 },   // Warm up
      { duration: '1m', target: 30 },    // Maintain baseline
      { duration: '10s', target: 300 },  // Sudden spike to 300 users
      { duration: '1m', target: 300 },   // Maintain spike
      { duration: '30s', target: 30 },   // Return to baseline
      { duration: '30s', target: 0 },    // Ramp down
    ],
    sleepBetweenRequests: { min: 0.1, max: 0.3 },
    thresholds: {
      http_req_duration: ['p(95)<1500', 'p(99)<3000'], // Very lenient during spike
    }
  }
};

// Get the test profile from environment variable or default to 'gentle'
const testProfile = __ENV.TEST_PROFILE || 'gentle';
const profile = testProfiles[testProfile];

if (!profile) {
  throw new Error(`Unknown test profile: ${testProfile}. Available profiles: ${Object.keys(testProfiles).join(', ')}`);
}

export const options = {
  stages: profile.stages,
  thresholds: profile.thresholds,
  summaryTrendStats: ['p(50)', 'p(90)', 'p(95)', 'p(99)'],
  insecureSkipTLSVerify: true,
};

export default function() {
  const baseUrl = __ENV.BASE_URL || 'https://localhost';
  
  const urls = [
    `${baseUrl}/`,                    // Static files
    `${baseUrl}/content/`,            // Article list
    `${baseUrl}/article/otel`,        // Single article
    `${baseUrl}/feed/`,               // RSS feed
  ];

  // Choose a random URL from the list
  const url = urls[Math.floor(Math.random() * urls.length)];
  
  // Make the request
  const response = http.get(url);
  
  // Check if the request was successful
  check(response, {
    'status is 200': (r) => r.status === 200,
  });
  
  // Record endpoint-specific durations
  if (url === `${baseUrl}/`) {
    staticFileDuration.add(response.timings.duration);
  } else if (url === `${baseUrl}/content/`) {
    articleListDuration.add(response.timings.duration);
  } else if (url.includes('/article/')) {
    singleArticleDuration.add(response.timings.duration);
  } else if (url === `${baseUrl}/feed/`) {
    rssFeedDuration.add(response.timings.duration);
  }
  
  // Sleep with profile-specific timing
  const sleepTime = profile.sleepBetweenRequests.min + 
                   (Math.random() * (profile.sleepBetweenRequests.max - profile.sleepBetweenRequests.min));
  sleep(sleepTime);
}

// Same handleSummary function as before
export function handleSummary(data) {
  // Helper function to format percentile data or show N/A if unavailable
  const formatPercentiles = (metricName) => {
    if (!data.metrics[metricName]) return "N/A";
    
    const m = data.metrics[metricName].values;
    return {
      'p50': m['p(50)']?.toFixed(2) || 'N/A',
      'p90': m['p(90)']?.toFixed(2) || 'N/A',
      'p95': m['p(95)']?.toFixed(2) || 'N/A',
      'p99': m['p(99)']?.toFixed(2) || 'N/A'
    };
  };

  // Create a simple summary with just the percentiles by endpoint
  const summary = {
    'Test Profile': __ENV.TEST_PROFILE || 'gentle',
    'Total Requests': data.metrics.http_reqs.values.count,
    'Endpoints': {
      'Static Files': formatPercentiles('static_file_duration'),
      'Article List': formatPercentiles('article_list_duration'),
      'Single Article': formatPercentiles('single_article_duration'),
      'RSS Feed': formatPercentiles('rss_feed_duration')
    }
  };
  
  // Create a formatted text summary
  const txtSummary = `
Test Profile: ${summary['Test Profile']}
Endpoint Latency Percentiles (ms)
================================
                  p50      p90      p95      p99
Static Files:    ${summary.Endpoints['Static Files'].p50.padEnd(8)} ${summary.Endpoints['Static Files'].p90.padEnd(8)} ${summary.Endpoints['Static Files'].p95.padEnd(8)} ${summary.Endpoints['Static Files'].p99}
Article List:    ${summary.Endpoints['Article List'].p50.padEnd(8)} ${summary.Endpoints['Article List'].p90.padEnd(8)} ${summary.Endpoints['Article List'].p95.padEnd(8)} ${summary.Endpoints['Article List'].p99}
Single Article:  ${summary.Endpoints['Single Article'].p50.padEnd(8)} ${summary.Endpoints['Single Article'].p90.padEnd(8)} ${summary.Endpoints['Single Article'].p95.padEnd(8)} ${summary.Endpoints['Single Article'].p99}
RSS Feed:        ${summary.Endpoints['RSS Feed'].p50.padEnd(8)} ${summary.Endpoints['RSS Feed'].p90.padEnd(8)} ${summary.Endpoints['RSS Feed'].p95.padEnd(8)} ${summary.Endpoints['RSS Feed'].p99}

Total Requests:  ${summary['Total Requests']}
`;

  return {
    'stdout': txtSummary,
    'summary.json': JSON.stringify(summary, null, 2),
    'summary.txt': txtSummary
  };
}
