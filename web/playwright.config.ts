import { defineConfig, devices } from '@playwright/test';
export default defineConfig({testDir:'./tests',workers:1,reporter:'list',use:{baseURL:process.env.RUBBERAI_TEST_URL||'http://localhost:8080',channel:'chrome',screenshot:'only-on-failure'},projects:[{name:'desktop',use:{...devices['Desktop Chrome']}},{name:'mobile',use:{viewport:{width:390,height:844},isMobile:true,hasTouch:true}}]});
