<#macro registrationLayout displayInfo=false displayMessage=true displayRequiredFields=false>
<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>${msg("loginTitle",(realm.displayName!''))}</title>
    <link rel="icon" href="${url.resourcesPath}/img/favicon.ico" />
    <style>
        @import url('https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&display=swap');
        
        body {
            margin: 0;
            padding: 0;
            font-family: 'Inter', sans-serif;
            background-color: white;
            height: 100vh;
            overflow: hidden;
        }
        .login-split-container {
            display: flex;
            width: 100vw;
            height: 100vh;
        }
        /* Left Side */
        .login-form-side {
            width: 50%;
            display: flex;
            flex-direction: column;
            justify-content: center;
            align-items: center;
            padding: 40px;
            position: relative;
        }
        .back-button {
            position: absolute;
            top: 30px;
            left: 30px;
            text-decoration: none;
            color: #4b5563;
            font-size: 14px;
            font-weight: 500;
            display: flex;
            align-items: center;
        }
        .login-card {
            width: 100%;
            max-width: 380px;
        }
        .login-header { text-align: center; margin-bottom: 30px; }
        .login-header h1 { font-size: 26px; font-weight: 700; color: #111827; margin: 0 0 8px 0; }
        .login-header p { font-size: 14px; color: #4b5563; line-height: 1.5; margin: 0; }
        
        .form-group { margin-bottom: 18px; }
        .form-group label { display: block; font-size: 13px; font-weight: 600; margin-bottom: 6px; color: #374151; }
        .input-wrapper { position: relative; display: flex; align-items: center; }
        .input-wrapper svg { position: absolute; left: 12px; color: #9ca3af; width: 16px; height: 16px; }
        .pf-c-form-control {
            width: 100% !important;
            padding: 10px 12px 10px 38px !important;
            border: 1px solid #d1d5db !important;
            border-radius: 8px !important;
            font-size: 14px !important;
            outline: none !important;
        }
        .pf-c-form-control:focus { border-color: #6D3E93 !important; ring: 1px solid #6D3E93 !important; }
        
        .forgot-password-link { display: block; text-align: right; font-size: 13px; color: #3b82f6; text-decoration: none; margin-top: 4px; }
        
        /* Submit Button */
        #kc-login {
            width: 100% !important;
            background: #6D3E93 !important;
            color: white !important;
            border: none !important;
            border-radius: 8px !important;
            padding: 10px !important;
            font-weight: 600 !important;
            font-size: 15px !important;
            cursor: pointer !important;
            margin-top: 15px !important;
        }
        
        /* Divider */
        .divider { display: flex; align-items: center; margin: 25px 0; color: #9ca3af; }
        .divider hr { flex: 1; border: 0; border-top: 1px solid #e5e7eb; }
        .divider span { padding: 0 10px; font-size: 12px; }
        
        /* Social */
        #kc-social-providers ul { display: flex; justify-content: center; gap: 15px; list-style: none; padding: 0; margin: 0; }
        .social-btn { 
            display: flex; align-items: center; justify-content: center; 
            width: 42px; height: 42px; border: 1px solid #e5e7eb; 
            border-radius: 8px; text-decoration: none; 
        }
        .social-btn:hover { background: #f9fafb; }
        .social-btn svg { width: 20px; height: 20px; }

        /* Right Side */
        .login-image-side {
            width: 50%;
            height: calc(100vh - 30px);
            margin: 15px;
            background: #333 url('${url.resourcesPath}/img/signin-bg.jpg') no-repeat center center;
            background-size: cover;
            border-radius: 20px;
            position: relative;
            overflow: hidden;
        }
        .login-image-overlay { position: absolute; inset: 0; background: linear-gradient(to top, rgba(0,0,0,0.5), transparent); }
        .login-testimonial { position: absolute; bottom: 40px; left: 40px; color: white; max-width: 400px; }
        .stars { color: #facc15; font-size: 18px; margin-bottom: 10px; }
        .login-testimonial h2 { font-size: 28px; font-weight: 700; margin: 0; }
        .login-testimonial p.desc { font-size: 14px; line-height: 1.6; margin: 10px 0 20px 0; opacity: 0.9; }
        .login-testimonial .directors { font-size: 14px; font-weight: 600; color: #60a5fa; margin: 0; }
        .login-testimonial .title { font-size: 12px; margin: 0; }
        
        .alert-error { background: #fee2e2; border: 1px solid #fca5a5; color: #b91c1c; padding: 10px; border-radius: 8px; margin-bottom: 15px; font-size: 13px; }

        /* Password Eye Toggle */
        .password-wrapper { position: relative; }
        .password-wrapper .pf-c-form-control { padding-right: 42px !important; }
        .eye-toggle {
            position: absolute;
            right: 10px;
            top: 50%;
            transform: translateY(-50%);
            background: none;
            border: none;
            cursor: pointer;
            color: #9ca3af;
            padding: 4px;
            display: flex;
            align-items: center;
            justify-content: center;
        }
        .eye-toggle:hover { color: #6D3E93; }
        .eye-icon { width: 18px; height: 18px; }

        @media (max-width: 768px) {
            .login-form-side { width: 100%; }
            .login-image-side { display: none; }
        }
    </style>
</head>

<body>
    <div class="login-split-container">
        <!-- Left Side -->
        <div class="login-form-side">
            <a href="javascript:history.back()" class="back-button">
                 <svg fill="none" viewBox="0 0 24 24" stroke-width="2" stroke="currentColor" style="width:16px; margin-right:5px;"><path stroke-linecap="round" stroke-linejoin="round" d="M10.5 19.5L3 12m0 0l7.5-7.5M3 12h18" /></svg>
                 Back
            </a>
            
            <div class="login-card">
                <#if displayMessage && message?has_content && (message.type != 'warning' || !isAppInitiatedAction??)>
                    <div class="alert-${message.type}">
                        ${kcSanitize(message.summary)?no_esc}
                    </div>
                </#if>

                <#nested "header">
                <div id="kc-content">
                    <div id="kc-content-wrapper">
                        <#nested "form">
                        <#nested "info">
                    </div>
                </div>
            </div>
        </div>

        <!-- Right Side -->
        <div class="login-image-side">
            <div class="login-image-overlay"></div>
            <div class="login-testimonial">
                <div class="stars">★★★★★</div>
                <h2>LeMiCi AI</h2>
                <p class="desc">
                    LeMiCi IQ is a marketing automation platform offering WhatsApp
                    Business API, web push, and social media tools.
                </p>
                <p class="directors">
                    Pritesh Prakash Rane & Soniya Uday Naik
                </p>
                <p class="title">Directors</p>
            </div>
        </div>
    </div>
</body>
<script>
    function togglePassword(inputId, btn) {
        var input = document.getElementById(inputId);
        var eyeOff = btn.querySelector('.eye-off');
        var eyeOn = btn.querySelector('.eye-on');
        if (input.type === 'password') {
            input.type = 'text';
            eyeOff.style.display = 'none';
            eyeOn.style.display = 'block';
        } else {
            input.type = 'password';
            eyeOff.style.display = 'block';
            eyeOn.style.display = 'none';
        }
    }
</script>
</html>
</#macro>
