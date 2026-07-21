<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Welcome to Lemici</title>
    <style>
        @import url('https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&display=swap');
        
        body {
            font-family: 'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            background-color: #f8fafc;
            margin: 0;
            padding: 0;
            -webkit-font-smoothing: antialiased;
            -moz-osx-font-smoothing: grayscale;
        }
        
        .email-container {
            width: 100%;
            background-color: #f8fafc;
            padding: 40px 20px;
        }
        
        .email-card {
            max-width: 580px;
            margin: 0 auto;
            background-color: #ffffff;
            border-radius: 16px;
            border: 1px solid #e2e8f0;
            box-shadow: 0 4px 6px -1px rgba(0, 0, 0, 0.05), 0 2px 4px -1px rgba(0, 0, 0, 0.03);
            overflow: hidden;
        }
        
        .email-header {
            padding: 32px 40px 24px 40px;
            border-bottom: 1px solid #f1f5f9;
            text-align: center;
        }
        
        .logo-img {
            height: 38px;
            vertical-align: middle;
            margin-right: 10px;
        }
        
        .brand-name {
            font-size: 22px;
            font-weight: 700;
            color: #0f172a;
            vertical-align: middle;
            display: inline-block;
            letter-spacing: -0.5px;
        }
        
        .email-body {
            padding: 40px;
        }
        
        .welcome-title {
            font-size: 24px;
            font-weight: 700;
            color: #0f172a;
            margin-top: 0;
            margin-bottom: 16px;
            letter-spacing: -0.5px;
        }
        
        .welcome-text {
            font-size: 15px;
            line-height: 1.6;
            color: #334155;
            margin-bottom: 24px;
        }
        
        .cta-container {
            text-align: center;
            margin: 36px 0;
        }
        
        .cta-button {
            display: inline-block;
            background-color: #6D3E93;
            color: #ffffff !important;
            padding: 14px 36px;
            font-size: 15px;
            font-weight: 600;
            text-decoration: none;
            border-radius: 10px;
            box-shadow: 0 4px 10px rgba(109, 62, 147, 0.25);
            transition: all 0.2s ease;
        }
        
        .expiry-note {
            font-size: 13px;
            line-height: 1.5;
            color: #64748b;
            border-top: 1px solid #f1f5f9;
            padding-top: 24px;
            margin-top: 36px;
        }
        
        .fallback-text {
            font-size: 12px;
            color: #94a3b8;
            margin-top: 16px;
            word-break: break-all;
            line-height: 1.4;
        }
        
        .fallback-link {
            color: #3b82f6;
            text-decoration: underline;
        }
        
        .email-footer {
            padding: 24px 40px;
            background-color: #f8fafc;
            border-top: 1px solid #f1f5f9;
            text-align: center;
        }
        
        .footer-text {
            font-size: 12px;
            color: #94a3b8;
            margin: 0 0 8px 0;
            line-height: 1.5;
        }
    </style>
</head>
<body>
    <div class="email-container">
        <div class="email-card">
            <!-- Header -->
            <div class="email-header">
                <img src="https://dev.lemici.com/abhinay/cube.png" alt="Lemici Logo" class="logo-img">
                <span class="brand-name">Lemici</span>
            </div>
            
            <!-- Body -->
            <div class="email-body">
                <h1 class="welcome-title">Welcome to Lemici, ${user.firstName!'User'}!</h1>
                <p class="welcome-text">We're absolutely thrilled to have you on board! Lemici is built to streamline your operations and automate your business workflows.</p>
                <p class="welcome-text">To secure your account and complete your registration, please verify your email address by clicking the verification link below:</p>
                
                <!-- CTA Button -->
                <div class="cta-container">
                    <a href="${link}" class="cta-button">Verify Email Address</a>
                </div>
                
                <!-- Expiry and Fallback Link -->
                <div class="expiry-note">
                    <p style="margin: 0 0 10px 0;"><strong>Please note:</strong> This verification link will expire in ${(linkExpiration/60000)?int} minutes. If you did not create a Lemici account, you can safely ignore this email.</p>
                    <div class="fallback-text">
                        If the button above does not work, please copy and paste this URL into your browser:<br>
                        <a href="${link}" class="fallback-link">${link}</a>
                    </div>
                </div>
            </div>
            
            <!-- Footer -->
            <div class="email-footer">
                <p class="footer-text">&copy; 2026 Lemici IQ. All rights reserved.</p>
                <p class="footer-text">This is an automated system email. Please do not reply directly to this message.</p>
            </div>
        </div>
    </div>
</body>
</html>
